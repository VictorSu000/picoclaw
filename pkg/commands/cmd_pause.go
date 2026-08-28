package commands

import (
	"context"
	"fmt"
)

func pauseCommand() Definition {
	return Definition{
		Name:        "pause",
		Description: "Pause the current task, keeping context for correction",
		Usage:       "/pause",
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			if rt == nil || rt.PauseActiveTurn == nil {
				return req.Reply(unavailableMsg)
			}

			result, err := rt.PauseActiveTurn()
			if err != nil {
				return req.Reply("Failed to pause task: " + err.Error())
			}

			return req.Reply(FormatPauseReply(result))
		},
	}
}

// FormatPauseReply renders a user-facing reply for a pause request.
func FormatPauseReply(result PauseResult) string {
	if !result.Paused {
		return "No active task to pause."
	}

	taskName := compactStopTaskName(result.TaskName)
	if taskName == "" {
		return "Task paused. Context kept — your next message continues from here."
	}

	return fmt.Sprintf("Task paused. %q was paused, context kept.", taskName)
}
