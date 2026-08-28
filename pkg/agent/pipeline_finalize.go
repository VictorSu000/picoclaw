// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"

	"github.com/sipeed/picoclaw/pkg/bus"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// Finalize handles turn finalization, either:
// - Early return when allResponsesHandled=true (ExecuteTools already finalized)
// - Normal finalization for allResponsesHandled=false (sets finalContent, saves session, compact)
func (p *Pipeline) Finalize(
	ctx context.Context,
	turnCtx context.Context,
	ts *turnState,
	exec *turnExecution,
	turnStatus TurnEndStatus,
	finalContent string,
) (turnResult, error) {
	al := p.al

	// When allResponsesHandled=true, ExecuteTools already finalized
	// (added handledToolResponseSummary, saved session, set phase to Completed).
	// But still check for hard abort - if requested, abort the turn.
	if exec.allResponsesHandled {
		if ts.hardAbortRequested() {
			return al.abortTurn(ts)
		}
		ts.setPhase(TurnPhaseCompleted)
		return turnResult{
			finalContent:  finalContent,
			modelName:     exec.llmModelName,
			status:        turnStatus,
			followUps:     append([]bus.InboundMessage(nil), ts.followUps...),
			emptyResponse: exec.emptyAfterRetries && finalContent == "",
		}, nil
	}

	ts.setPhase(TurnPhaseFinalizing)
	ts.setFinalContent(finalContent)
	if !ts.opts.NoHistory {
		finalMsg := providers.Message{
			Role:             "assistant",
			Content:          finalContent,
			ModelName:        exec.llmModelName,
			ReasoningContent: responseReasoningContent(exec.response),
		}
		ts.agent.Sessions.AddFullMessage(ts.sessionKey, finalMsg)
		ts.recordPersistedMessage(finalMsg)
		ts.ingestMessage(turnCtx, al, finalMsg)
		if err := ts.agent.Sessions.Save(ts.sessionKey); err != nil {
			al.emitEvent(
				runtimeevents.KindAgentError,
				ts.eventMeta("runTurn", "turn.error"),
				ErrorPayload{
					Stage:   "session_save",
					Message: err.Error(),
				},
			)
			cancelConfiguredStreamingLLM(turnCtx, exec)
			return turnResult{status: TurnEndStatusError}, err
		}

		// If the user paused this turn, inject a persistent note into the
		// session history after the agent's summary so subsequent turns can
		// tell where work was interrupted. A "user" role is used (not "system")
		// because sanitizeHistoryForProvider drops system messages before they
		// reach the provider; a user-role note survives sanitization and is
		// visible to the model on the next turn.
		if al.takePauseMarker(ts.sessionKey) {
			marker := providers.Message{
				Role:    "user",
				Content: "[The user paused this task. The above summary marks where work stopped. Await the user's next instruction.]",
			}
			ts.agent.Sessions.AddFullMessage(ts.sessionKey, marker)
			ts.recordPersistedMessage(marker)
			if err := ts.agent.Sessions.Save(ts.sessionKey); err != nil {
				al.emitEvent(
					runtimeevents.KindAgentError,
					ts.eventMeta("runTurn", "turn.error"),
					ErrorPayload{
						Stage:   "session_save",
						Message: err.Error(),
					},
				)
				cancelConfiguredStreamingLLM(turnCtx, exec)
				return turnResult{status: TurnEndStatusError}, err
			}
		}
	}

	if !ts.opts.NoHistory && ts.opts.EnableSummary {
		al.contextManager.Compact(
			turnCtx,
			&CompactRequest{
				SessionKey: ts.sessionKey,
				Reason:     ContextCompressReasonSummarize,
				Budget:     ts.agent.ContextWindow,
			},
		)
	}

	contextUsage := computeContextUsage(ts.agent, ts.sessionKey)
	streamErr := finalizeConfiguredStreamingLLM(turnCtx, ts, exec, finalContent, contextUsage)
	// If streaming never became visible, keep the legacy Pico interim publish path
	// so the final answer is still delivered outside normal SendResponse.
	if ((streamErr != nil && !isConfiguredStreamingVisibleError(streamErr)) || exec.streamingFallback) &&
		!ts.opts.SendResponse && ts.opts.AllowInterimPicoPublish && finalContent != "" {
		msg := outboundMessageForTurnWithOptions(ts, finalContent, outboundTurnMessageOptions{
			modelName: exec.llmModelName,
		})
		msg.ContextUsage = contextUsage
		markFinalOutbound(&msg)
		_ = al.bus.PublishOutbound(turnCtx, msg)
	}
	if streamErr != nil && isConfiguredStreamingVisibleError(streamErr) {
		ts.setPhase(TurnPhaseCompleted)
		return turnResult{
			finalContent: finalContent,
			status:       TurnEndStatusError,
			followUps:    append([]bus.InboundMessage(nil), ts.followUps...),
		}, streamErr
	}
	ts.setPhase(TurnPhaseCompleted)
	return turnResult{
		finalContent:  finalContent,
		modelName:     exec.llmModelName,
		status:        turnStatus,
		followUps:     append([]bus.InboundMessage(nil), ts.followUps...),
		emptyResponse: exec.emptyAfterRetries && finalContent == "",
	}, nil
}
