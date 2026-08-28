package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/commands"
)

func (al *AgentLoop) tryHandlePauseCommand(
	ctx context.Context,
	msg bus.InboundMessage,
	sessionKey string,
) bool {
	cmdName, ok := commands.CommandName(msg.Content)
	if !ok || cmdName != "pause" {
		return false
	}

	result, err := al.pauseActiveTurnForSession(sessionKey)

	// This function is only called when loaded=true (another turn already
	// claimed this session). If pauseActiveTurnForSession found a pending
	// placeholder but didn't pause it, that placeholder belongs to the other
	// message's worker which hasn't started yet — arm a pending pause so the
	// worker will bail when it checks before running.
	if err == nil && !result.Paused {
		if ts := al.getActiveTurnState(sessionKey); ts != nil {
			snap := ts.snapshot()
			if strings.HasPrefix(snap.TurnID, pendingTurnPrefix) {
				al.markPendingPause(sessionKey)
				result.Paused = true
			}
		}
	}

	reply := commands.FormatPauseReply(result)
	if err != nil {
		reply = "Failed to pause task: " + err.Error()
	}

	if al.channelManager != nil {
		al.channelManager.InvokeTypingStop(msg.Channel, msg.ChatID)
	}
	al.PublishResponseIfNeeded(ctx, msg.Channel, msg.ChatID, sessionKey, reply)
	return true
}

func (al *AgentLoop) pauseActiveTurnForSession(sessionKey string) (commands.PauseResult, error) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return commands.PauseResult{}, fmt.Errorf("session key is required")
	}

	result := commands.PauseResult{}
	cleared := al.clearSteeringMessagesForScope(sessionKey)
	al.clearPendingSkills(sessionKey)

	ts := al.getActiveTurnState(sessionKey)
	if ts == nil {
		result.Paused = cleared > 0
		return result, nil
	}

	snap := ts.snapshot()
	result.TaskName = snap.UserMessage

	if strings.HasPrefix(snap.TurnID, pendingTurnPrefix) {
		// A pending placeholder means this session is either idle (our own
		// placeholder from the /pause command) or another message is queued but
		// hasn't started yet. In both cases, we don't arm a pending pause here;
		// the caller (tryHandlePauseCommand) handles the "another message queued"
		// case explicitly, since it knows loaded=true.
		return result, nil
	}

	// Graceful interrupt: unlike HardAbort, this does not cancel the provider
	// context. The current LLM response / tool execution runs to completion,
	// then the agent produces a short summary and ends the turn normally,
	// preserving session history for the user's follow-up correction.
	if !ts.requestGracefulInterrupt("") {
		return commands.PauseResult{}, fmt.Errorf("turn %s cannot accept graceful interrupt", snap.TurnID)
	}

	// Arm a persistent marker so Finalize injects a system note into the
	// session history after the agent's summary is saved.
	al.markPauseMarker(sessionKey)
	result.Paused = true
	return result, nil
}

// ====================== Pending Pause (for not-yet-started workers) ======================

func (al *AgentLoop) markPendingPause(sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	al.pendingPauses.Store(sessionKey, struct{}{})
}

func (al *AgentLoop) takePendingPause(sessionKey string) bool {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return false
	}
	_, ok := al.pendingPauses.LoadAndDelete(sessionKey)
	return ok
}

// ====================== Pause Marker (system note into history) ======================

func (al *AgentLoop) markPauseMarker(sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	al.pauseMarkers.Store(sessionKey, struct{}{})
}

func (al *AgentLoop) takePauseMarker(sessionKey string) bool {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return false
	}
	_, ok := al.pauseMarkers.LoadAndDelete(sessionKey)
	return ok
}
