package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
)

type staticSessionTitleProvider struct {
	content string
}

func (p *staticSessionTitleProvider) Chat(
	context.Context,
	[]providers.Message,
	[]providers.ToolDefinition,
	string,
	map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: p.content}, nil
}

func (p *staticSessionTitleProvider) GetDefaultModel() string {
	return "fast-model-id"
}

func TestNormalizeGeneratedSessionTitle(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain", input: "A short title", want: "A short title"},
		{name: "label and quotes", input: `Title: "A short title"`, want: "A short title"},
		{name: "chinese label", input: "标题：会话自动标题\n这里是解释", want: "会话自动标题"},
		{name: "markdown", input: "## `Model configuration`", want: "Model configuration"},
		{name: "empty", input: " \n ", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeGeneratedSessionTitle(tt.input); got != tt.want {
				t.Fatalf("normalizeGeneratedSessionTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeGeneratedSessionTitle_TruncatesRunes(t *testing.T) {
	got := normalizeGeneratedSessionTitle(strings.Repeat("题", sessionTitleMaxRunes+10))
	if len([]rune(got)) != sessionTitleMaxRunes {
		t.Fatalf("title rune length = %d, want %d", len([]rune(got)), sessionTitleMaxRunes)
	}
}

func TestShouldGenerateFirstSessionTitle(t *testing.T) {
	store, err := memory.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	backend := session.NewJSONLBackend(store)
	agent := &AgentInstance{
		FastModelID:  "fast-model-id",
		FastProvider: &mockProvider{},
		Sessions:     backend,
	}
	opts := processOptions{
		Dispatch: DispatchRequest{
			SessionKey: "session-1",
			InboundContext: &bus.InboundContext{
				Channel: "pico",
			},
		},
		UserMessage: "hello",
	}

	if !shouldGenerateFirstSessionTitle(agent, opts) {
		t.Fatal("empty Pico session should be eligible for a title")
	}
	backend.AddMessage("session-1", "user", "an earlier failed attempt")
	if !shouldGenerateFirstSessionTitle(agent, opts) {
		t.Fatal("user-only history should remain eligible until the first assistant reply")
	}
	backend.AddMessage("session-1", "assistant", "reply")
	if shouldGenerateFirstSessionTitle(agent, opts) {
		t.Fatal("session with an assistant reply should not be eligible")
	}

	opts.Dispatch.SessionKey = "session-2"
	opts.Dispatch.InboundContext.Channel = "telegram"
	if shouldGenerateFirstSessionTitle(agent, opts) {
		t.Fatal("non-Pico session should not be eligible")
	}
}

func TestScheduleSessionTitle_WritesConditionally(t *testing.T) {
	store, err := memory.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	backend := session.NewJSONLBackend(store)
	agent := &AgentInstance{
		ID:           "main",
		FastModel:    "fast-model",
		FastModelID:  "fast-model-id",
		FastProvider: &staticSessionTitleProvider{content: `Title: "Generated title"`},
		Sessions:     backend,
	}
	al := &AgentLoop{}
	al.activeReqCond = sync.NewCond(&al.activeReqMu)
	opts := processOptions{
		Dispatch:    DispatchRequest{SessionKey: "session-1"},
		UserMessage: "Help me configure a model",
	}

	al.scheduleSessionTitle(agent, opts, "Here is the configuration.")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !al.waitForActiveRequests(ctx, time.Second) {
		t.Fatal("title generation did not finish")
	}
	meta, err := store.GetSessionMeta(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("GetSessionMeta() error = %v", err)
	}
	if meta.Title != "Generated title" {
		t.Fatalf("generated title = %q, want %q", meta.Title, "Generated title")
	}

	if _, err := store.SetSessionTitle(context.Background(), "session-2", "Manual title", true); err != nil {
		t.Fatalf("SetSessionTitle() error = %v", err)
	}
	opts.Dispatch.SessionKey = "session-2"
	al.scheduleSessionTitle(agent, opts, "Here is the configuration.")
	if !al.waitForActiveRequests(ctx, time.Second) {
		t.Fatal("second title generation did not finish")
	}
	meta, err = store.GetSessionMeta(context.Background(), "session-2")
	if err != nil {
		t.Fatalf("GetSessionMeta() error = %v", err)
	}
	if meta.Title != "Manual title" {
		t.Fatalf("title after generation = %q, want manual title preserved", meta.Title)
	}
}
