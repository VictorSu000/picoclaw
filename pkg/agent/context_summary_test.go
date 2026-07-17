package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestBuildContextSummaryUsesCompressionPromptAndExistingContext(t *testing.T) {
	messages := []providers.Message{
		{Role: "system", Content: "not summarized"},
		{Role: "user", Content: "question"},
		{Role: "tool", Content: "tool output"},
		{Role: "assistant", Content: "answer"},
	}
	var prompt string
	result := BuildContextSummary(
		context.Background(),
		messages,
		"prior summary",
		8000,
		func(_ context.Context, value string) (string, error) {
			prompt = value
			return "  rebuilt summary  ", nil
		},
	)

	if result.Summary != "rebuilt summary" {
		t.Fatalf("Summary = %q, want trimmed provider response", result.Summary)
	}
	if result.SummarizedMessages != 2 {
		t.Fatalf("SummarizedMessages = %d, want 2", result.SummarizedMessages)
	}
	if !strings.HasPrefix(prompt, "Provide a concise summary of this conversation segment, preserving core context and key points.\n") ||
		!strings.Contains(prompt, "Existing context: prior summary\n") ||
		!strings.Contains(prompt, "\nCONVERSATION:\nuser: question\nassistant: answer\n") ||
		strings.Contains(prompt, "not summarized") ||
		strings.Contains(prompt, "tool output") {
		t.Fatalf("unexpected prompt = %q", prompt)
	}
}

func TestBuildContextSummarySplitsAndMergesLongHistory(t *testing.T) {
	messages := make([]providers.Message, 0, 12)
	for i := 0; i < 6; i++ {
		messages = append(messages,
			providers.Message{Role: "user", Content: fmt.Sprintf("question %d", i)},
			providers.Message{Role: "assistant", Content: fmt.Sprintf("answer %d", i)},
		)
	}
	prompts := make([]string, 0, 3)
	result := BuildContextSummary(
		context.Background(),
		messages,
		"must not be reused for split archive summaries",
		8000,
		func(_ context.Context, prompt string) (string, error) {
			prompts = append(prompts, prompt)
			switch len(prompts) {
			case 1:
				return "first half", nil
			case 2:
				return "second half", nil
			default:
				return "merged summary", nil
			}
		},
	)

	if result.Summary != "merged summary" {
		t.Fatalf("Summary = %q, want merged summary", result.Summary)
	}
	if len(prompts) != 3 {
		t.Fatalf("completion calls = %d, want 3", len(prompts))
	}
	if strings.Contains(prompts[0], "Existing context:") || strings.Contains(prompts[1], "Existing context:") {
		t.Fatalf("split prompts unexpectedly reused existing summary: %#v", prompts[:2])
	}
	if !strings.Contains(prompts[2], "1: first half") || !strings.Contains(prompts[2], "2: second half") {
		t.Fatalf("unexpected merge prompt = %q", prompts[2])
	}
}

func TestBuildContextSummaryUsesOriginalFallback(t *testing.T) {
	result := BuildContextSummary(
		context.Background(),
		[]providers.Message{
			{Role: "user", Content: "question"},
			{Role: "assistant", Content: "answer"},
		},
		"stale summary",
		8000,
		nil,
	)

	if result.Summary != "Conversation summary: user: question | assistant: answer" {
		t.Fatalf("Summary = %q", result.Summary)
	}
}
