package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
)

// legacyContextManager wraps the existing summarization/compression logic
// as a ContextManager implementation. It is the default when no other
// ContextManager is configured.
type legacyContextManager struct {
	al          *AgentLoop
	summarizing sync.Map // dedup for async Compact (post-turn)
}

func (m *legacyContextManager) Assemble(_ context.Context, req *AssembleRequest) (*AssembleResponse, error) {
	// Legacy: read history from session, return as-is.
	// Budget enforcement happens in BuildMessages caller via
	// isOverContextBudget + forceCompression.
	agent := m.al.registry.GetDefaultAgent()
	if agent == nil {
		return &AssembleResponse{}, nil
	}
	history := agent.Sessions.GetHistory(req.SessionKey)
	summary := agent.Sessions.GetSummary(req.SessionKey)
	return &AssembleResponse{
		History: history,
		Summary: summary,
	}, nil
}

func (m *legacyContextManager) Compact(_ context.Context, req *CompactRequest) error {
	switch req.Reason {
	case ContextCompressReasonProactive, ContextCompressReasonRetry:
		// Sync emergency compression — budget exceeded.
		if result, ok := m.forceCompression(req.SessionKey); ok {
			m.al.emitEvent(
				runtimeevents.KindAgentContextCompress,
				m.al.newTurnEventScope("", req.SessionKey, nil).meta(0, "forceCompression", "turn.context.compress"),
				ContextCompressPayload{
					Reason:            req.Reason,
					DroppedMessages:   result.DroppedMessages,
					RemainingMessages: result.RemainingMessages,
				},
			)
		}
	case ContextCompressReasonSummarize:
		m.maybeSummarize(req.SessionKey)
	}
	return nil
}

func (m *legacyContextManager) Ingest(_ context.Context, _ *IngestRequest) error {
	// Legacy: no-op. Messages are persisted by Sessions JSONL.
	return nil
}

func (m *legacyContextManager) Clear(_ context.Context, sessionKey string) error {
	agent := m.al.registry.GetDefaultAgent()
	if agent == nil || agent.Sessions == nil {
		return fmt.Errorf("sessions not initialized")
	}
	agent.Sessions.SetHistory(sessionKey, []providers.Message{})
	agent.Sessions.SetSummary(sessionKey, "")
	return agent.Sessions.Save(sessionKey)
}

// maybeSummarize triggers summarization if the session history exceeds thresholds.
// It runs asynchronously in a goroutine.
func (m *legacyContextManager) maybeSummarize(sessionKey string) {
	agent := m.al.registry.GetDefaultAgent()
	if agent == nil {
		return
	}

	newHistory := agent.Sessions.GetHistory(sessionKey)
	tokenEstimate := m.estimateTokens(newHistory)
	threshold := agent.ContextWindow * agent.SummarizeTokenPercent / 100

	if len(newHistory) > agent.SummarizeMessageThreshold || tokenEstimate > threshold {
		summarizeKey := agent.ID + ":" + sessionKey
		if _, loading := m.summarizing.LoadOrStore(summarizeKey, true); !loading {
			go func() {
				defer m.summarizing.Delete(summarizeKey)
				defer func() {
					if r := recover(); r != nil {
						logger.WarnCF("agent", "Summarization panic recovered", map[string]any{
							"session_key": sessionKey,
							"panic":       r,
						})
					}
				}()
				logger.Debug("Memory threshold reached. Optimizing conversation history...")
				m.summarizeSession(agent, sessionKey)
			}()
		}
	}
}

type compressionResult struct {
	DroppedMessages   int
	RemainingMessages int
}

// archiveDropped preserves messages that compaction is about to drop so the
// Web UI can still display the full conversation. It is a no-op when the
// session store does not implement archiving (e.g. the in-memory subturn
// store). Archived messages are never returned by GetHistory, so they never
// re-enter the LLM context.
func archiveDropped(sessions session.SessionStore, sessionKey string, dropped []providers.Message) {
	if len(dropped) == 0 {
		return
	}
	if archiver, ok := sessions.(session.ArchivingSessionStore); ok {
		archiver.ArchiveMessages(sessionKey, dropped)
	}
}

// forceCompression aggressively reduces context when the limit is hit.
// It drops the oldest ~50% of Turns (a Turn is a complete user→LLM→response
// cycle, as defined in #1316), so tool-call sequences are never split.
func (m *legacyContextManager) forceCompression(sessionKey string) (compressionResult, bool) {
	agent := m.al.registry.GetDefaultAgent()
	if agent == nil {
		return compressionResult{}, false
	}

	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) <= 2 {
		return compressionResult{}, false
	}

	turns := parseTurnBoundaries(history)
	var mid int
	if len(turns) >= 2 {
		mid = turns[len(turns)/2]
	} else {
		mid = findSafeBoundary(history, len(history)/2)
	}

	// cut is the boundary index: kept = history[cut:], dropped = history[:cut].
	// Keeping a clean suffix (rather than a lone user message) lets the dropped
	// prefix be archived in chronological order for Web UI display.
	cut := mid
	if cut <= 0 {
		// No safe turn boundary near the midpoint. Fall back to the last user
		// message so the kept suffix still starts at a turn boundary. If none
		// exists (single-turn history), cut stays 0 and nothing is dropped.
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].Role == "user" {
				cut = i
				break
			}
		}
	}
	keptHistory := history[cut:]
	dropped := history[:cut]

	droppedCount := len(dropped)

	existingSummary := agent.Sessions.GetSummary(sessionKey)
	compressionNote := fmt.Sprintf(
		"[Emergency compression dropped %d oldest messages due to context limit]",
		droppedCount,
	)
	if existingSummary != "" {
		compressionNote = existingSummary + "\n\n" + compressionNote
	}
	agent.Sessions.SetSummary(sessionKey, compressionNote)

	// Archive the dropped prefix BEFORE rewriting the active history, so the
	// messages survive the SetHistory rewrite and remain viewable in the Web UI.
	archiveDropped(agent.Sessions, sessionKey, dropped)

	agent.Sessions.SetHistory(sessionKey, keptHistory)
	agent.Sessions.Save(sessionKey)

	logger.WarnCF("agent", "Forced compression executed", map[string]any{
		"session_key":  sessionKey,
		"dropped_msgs": droppedCount,
		"new_count":    len(keptHistory),
	})

	return compressionResult{
		DroppedMessages:   droppedCount,
		RemainingMessages: len(keptHistory),
	}, true
}

func (m *legacyContextManager) summarizeSession(agent *AgentInstance, sessionKey string) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	history := agent.Sessions.GetHistory(sessionKey)
	summary := agent.Sessions.GetSummary(sessionKey)

	if len(history) <= 4 {
		return
	}

	safeCut := findSafeBoundary(history, len(history)-4)
	if safeCut <= 0 {
		return
	}
	keepCount := len(history) - safeCut
	toSummarize := history[:safeCut]

	provider := withCurrentCandidateRateLimit(agent.Provider, m.al, agent.Candidates)
	complete := func(ctx context.Context, prompt string) (string, error) {
		m.al.activeRequestsInc()
		defer m.al.activeRequestsDec()
		resp, err := provider.Chat(
			ctx,
			[]providers.Message{{Role: "user", Content: prompt}},
			nil,
			agent.Model,
			map[string]any{
				"max_tokens":       agent.MaxTokens,
				"temperature":      ContextSummaryTemperature(),
				"prompt_cache_key": agent.ID,
			},
		)
		if resp == nil {
			return "", err
		}
		return resp.Content, err
	}
	result := BuildContextSummary(
		ctx,
		toSummarize,
		summary,
		agent.ContextWindow,
		complete,
	)
	finalSummary := result.Summary

	if finalSummary != "" {
		agent.Sessions.SetSummary(sessionKey, finalSummary)
		// Archive the messages being dropped BEFORE truncating, so they survive
		// the TruncateHistory + Compact that follows and stay viewable in the
		// Web UI. history[:safeCut] is the exact dropped set (including tool
		// messages), matching TruncateHistory(keepCount) which keeps history[safeCut:].
		archiveDropped(agent.Sessions, sessionKey, toSummarize)
		agent.Sessions.TruncateHistory(sessionKey, keepCount)
		agent.Sessions.Save(sessionKey)
		m.al.emitEvent(
			runtimeevents.KindAgentSessionSummarize,
			m.al.newTurnEventScope(agent.ID, sessionKey, nil).meta(0, "summarizeSession", "turn.session.summarize"),
			SessionSummarizePayload{
				SummarizedMessages: result.SummarizedMessages,
				KeptMessages:       keepCount,
				SummaryLen:         len(finalSummary),
				OmittedOversized:   result.OmittedOversized,
			},
		)
	}
}

func (m *legacyContextManager) estimateTokens(messages []providers.Message) int {
	total := 0
	for _, msg := range messages {
		total += EstimateMessageTokens(msg)
	}
	return total
}
