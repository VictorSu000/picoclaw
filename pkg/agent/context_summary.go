package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

const (
	contextSummaryMaxMessages       = 10
	contextSummaryMaxRetries        = 3
	contextSummaryTemperature       = 0.3
	fallbackSummaryMinContentLength = 200
	fallbackSummaryContentPercent   = 10
)

// ContextSummaryCompletion performs one LLM completion for a summary prompt.
// BuildContextSummary owns retry, batching, merge, and fallback behavior so all
// callers use the same context-compression semantics.
type ContextSummaryCompletion func(ctx context.Context, prompt string) (string, error)

// ContextSummaryResult describes a summary built from conversation messages.
type ContextSummaryResult struct {
	Summary            string
	SummarizedMessages int
	OmittedOversized   bool
}

// ContextSummaryTemperature is the temperature used by context compression.
// Callers that provide the LLM completion apply this value to provider options.
func ContextSummaryTemperature() float64 {
	return contextSummaryTemperature
}

// BuildContextSummary creates a context summary using the legacy context
// compression strategy. Only user and assistant messages are summarized;
// oversized messages are omitted, long histories are summarized in two parts
// and merged, and failed LLM calls fall back to a deterministic local summary.
func BuildContextSummary(
	ctx context.Context,
	messages []providers.Message,
	existingSummary string,
	contextWindow int,
	complete ContextSummaryCompletion,
) ContextSummaryResult {
	maxMessageTokens := contextWindow / 2
	validMessages := make([]providers.Message, 0, len(messages))
	omitted := false

	for _, msg := range messages {
		if msg.Role != "user" && msg.Role != "assistant" {
			continue
		}
		msgTokens := len(msg.Content) / 2
		if msgTokens > maxMessageTokens {
			omitted = true
			continue
		}
		validMessages = append(validMessages, msg)
	}

	result := ContextSummaryResult{
		SummarizedMessages: len(validMessages),
		OmittedOversized:   omitted,
	}
	if len(validMessages) == 0 {
		return result
	}

	if len(validMessages) > contextSummaryMaxMessages {
		mid := nearestUserMessageIndex(validMessages, len(validMessages)/2)
		part1 := validMessages[:mid]
		part2 := validMessages[mid:]

		summary1 := summarizeContextBatch(ctx, part1, "", complete)
		summary2 := summarizeContextBatch(ctx, part2, "", complete)
		mergePrompt := fmt.Sprintf(
			"Merge these two conversation summaries into one cohesive summary:\n\n1: %s\n\n2: %s",
			summary1,
			summary2,
		)
		merged, err := retryContextSummaryCompletion(ctx, mergePrompt, complete)
		if err == nil && merged != "" {
			result.Summary = merged
		} else {
			result.Summary = summary1 + " " + summary2
		}
	} else {
		result.Summary = summarizeContextBatch(ctx, validMessages, existingSummary, complete)
	}

	if omitted && result.Summary != "" {
		result.Summary += "\n[Note: Some oversized messages were omitted from this summary for efficiency.]"
	}
	return result
}

func nearestUserMessageIndex(messages []providers.Message, mid int) int {
	originalMid := mid
	for mid > 0 && messages[mid].Role != "user" {
		mid--
	}
	if messages[mid].Role == "user" {
		return mid
	}

	mid = originalMid
	for mid < len(messages) && messages[mid].Role != "user" {
		mid++
	}
	if mid < len(messages) {
		return mid
	}
	return originalMid
}

func retryContextSummaryCompletion(
	ctx context.Context,
	prompt string,
	complete ContextSummaryCompletion,
) (string, error) {
	if complete == nil {
		return "", errors.New("context summary completion is unavailable")
	}

	var content string
	var err error
	for attempt := 0; attempt < contextSummaryMaxRetries; attempt++ {
		content, err = complete(ctx, prompt)
		if err == nil && content != "" {
			return content, nil
		}
		if attempt < contextSummaryMaxRetries-1 {
			delay := time.Duration(attempt+1) * 100 * time.Millisecond
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", ctx.Err()
			case <-timer.C:
			}
		}
	}
	if err == nil {
		err = errors.New("context summary completion returned empty content")
	}
	return content, err
}

func summarizeContextBatch(
	ctx context.Context,
	batch []providers.Message,
	existingSummary string,
	complete ContextSummaryCompletion,
) string {
	var prompt strings.Builder
	prompt.WriteString("Provide a concise summary of this conversation segment, preserving core context and key points.\n")
	if existingSummary != "" {
		prompt.WriteString("Existing context: ")
		prompt.WriteString(existingSummary)
		prompt.WriteString("\n")
	}
	prompt.WriteString("\nCONVERSATION:\n")
	for _, msg := range batch {
		fmt.Fprintf(&prompt, "%s: %s\n", msg.Role, msg.Content)
	}

	content, err := retryContextSummaryCompletion(ctx, prompt.String(), complete)
	if err == nil && content != "" {
		return strings.TrimSpace(content)
	}

	var fallback strings.Builder
	fallback.WriteString("Conversation summary: ")
	for i, msg := range batch {
		if i > 0 {
			fallback.WriteString(" | ")
		}
		content := strings.TrimSpace(msg.Content)
		runes := []rune(content)
		if len(runes) == 0 {
			fmt.Fprintf(&fallback, "%s: ", msg.Role)
			continue
		}

		keepLength := len(runes) * fallbackSummaryContentPercent / 100
		if keepLength < fallbackSummaryMinContentLength {
			keepLength = fallbackSummaryMinContentLength
		}
		if keepLength > len(runes) {
			keepLength = len(runes)
		}

		content = string(runes[:keepLength])
		if keepLength < len(runes) {
			content += "..."
		}
		fmt.Fprintf(&fallback, "%s: %s", msg.Role, content)
	}
	return fallback.String()
}
