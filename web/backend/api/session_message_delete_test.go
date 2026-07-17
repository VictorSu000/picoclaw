package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
)

type recordingContextSummaryProvider struct {
	response string
	err      error
	prompts  []string
	models   []string
	options  []map[string]any
}

func (p *recordingContextSummaryProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	model string,
	options map[string]any,
) (*providers.LLMResponse, error) {
	if len(messages) > 0 {
		p.prompts = append(p.prompts, messages[0].Content)
	}
	p.models = append(p.models, model)
	p.options = append(p.options, options)
	if p.err != nil {
		return nil, p.err
	}
	return &providers.LLMResponse{Content: p.response}, nil
}

func (p *recordingContextSummaryProvider) GetDefaultModel() string {
	return "summary-model"
}

func deleteMessageSeriesRequest(
	t *testing.T,
	configPath string,
	sessionID string,
	transcriptIndex int,
) (*httptest.ResponseRecorder, sessionDetailResponse) {
	t.Helper()

	h := NewHandler(configPath)
	return deleteMessageSeriesRequestWithHandler(t, h, sessionID, transcriptIndex)
}

func deleteMessageSeriesRequestWithHandler(
	t *testing.T,
	h *Handler,
	sessionID string,
	transcriptIndex int,
) (*httptest.ResponseRecorder, sessionDetailResponse) {
	t.Helper()

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodDelete,
		"/api/sessions/"+sessionID+"/message-series?transcript_index="+
			strconv.Itoa(transcriptIndex),
		nil,
	)
	mux.ServeHTTP(rec, req)

	var response sessionDetailResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("Unmarshal(response) error = %v", err)
		}
	}
	return rec, response
}

func TestHandleDeleteMessageSeries_RemovesToolCallSeries(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	sessionID := "delete-tool-series"
	sessionKey := legacyPicoSessionPrefix + sessionID
	messages := []providers.Message{
		{Role: "user", Content: "first user"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "inspect a file"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: &providers.FunctionCall{
					Name:      "read_file",
					Arguments: `{"path":"README.md"}`,
				},
			}},
		},
		{Role: "tool", ToolCallID: "call_1", Content: "secret tool output"},
		{Role: "assistant", Content: "answer based on the file"},
		{Role: "user", Content: "next user"},
		{Role: "assistant", Content: "next answer"},
	}
	for _, msg := range messages {
		if err := store.AddFullMessage(nil, sessionKey, msg); err != nil {
			t.Fatalf("AddFullMessage() error = %v", err)
		}
	}

	// Transcript: first user, first answer, inspect, tool_calls, file answer,
	// next user, next answer. Deleting index 4 removes the complete tool series.
	rec, response := deleteMessageSeriesRequest(t, configPath, sessionID, 4)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(response.Messages) != 5 {
		t.Fatalf("len(response.Messages) = %d, want 5", len(response.Messages))
	}

	history, err := store.GetHistory(nil, sessionKey)
	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
	if len(history) != 5 {
		t.Fatalf("len(history) = %d, want 5", len(history))
	}
	for _, msg := range history {
		if len(msg.ToolCalls) > 0 || msg.Role == "tool" || msg.Content == "answer based on the file" {
			t.Fatalf("deleted tool series remains in history: %#v", msg)
		}
	}
}

func TestHandleDeleteMessageSeries_MessageToolIncludesHiddenCompletion(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	sessionID := "delete-message-tool-series"
	sessionKey := legacyPicoSessionPrefix + sessionID
	for _, msg := range []providers.Message{
		{Role: "user", Content: "send an update"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{{
				ID:   "call_message",
				Type: "function",
				Function: &providers.FunctionCall{
					Name:      "message",
					Arguments: `{"content":"update delivered"}`,
				},
			}},
		},
		{Role: "tool", ToolCallID: "call_message", Content: "Message sent"},
		{Role: "assistant", Content: handledToolResponseSummaryText},
	} {
		if err := store.AddFullMessage(nil, sessionKey, msg); err != nil {
			t.Fatalf("AddFullMessage() error = %v", err)
		}
	}

	// Transcript: user, tool_calls, visible message-tool output.
	rec, response := deleteMessageSeriesRequest(t, configPath, sessionID, 2)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(response.Messages) != 1 || response.Messages[0].Content != "send an update" {
		t.Fatalf("response.Messages = %#v, want only the user message", response.Messages)
	}
	history, err := store.GetHistory(nil, sessionKey)
	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("len(history) = %d, want 1", len(history))
	}
}

func TestHandleDeleteMessageSeries_RebuildsSummaryFromRemainingArchive(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	sessionID := "delete-archived-series"
	sessionKey := legacyPicoSessionPrefix + sessionID
	for _, msg := range []providers.Message{
		{Role: "user", Content: "active user"},
		{Role: "assistant", Content: "active answer"},
	} {
		if err := store.AddFullMessage(nil, sessionKey, msg); err != nil {
			t.Fatalf("AddFullMessage() error = %v", err)
		}
	}
	archived := []providers.Message{
		{Role: "user", Content: "keep archived user"},
		{Role: "assistant", Content: "keep archived answer"},
		{Role: "user", Content: "deleted archived user"},
		{Role: "assistant", Content: "deleted archived answer"},
	}
	if err := store.ArchiveMessages(nil, sessionKey, archived); err != nil {
		t.Fatalf("ArchiveMessages() error = %v", err)
	}
	if err := store.SetSummary(nil, sessionKey, "stale summary containing deleted archived answer"); err != nil {
		t.Fatalf("SetSummary() error = %v", err)
	}
	provider := &recordingContextSummaryProvider{response: "LLM summary of remaining archive"}
	h := NewHandler(configPath)
	h.contextSummaryProvider = func(_ *config.Config) (providers.LLMProvider, string, error) {
		return provider, "summary-model", nil
	}

	// Combined transcript starts with the four archived entries.
	rec, response := deleteMessageSeriesRequestWithHandler(t, h, sessionID, 3)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if response.ArchivedCount != 3 {
		t.Fatalf("response.ArchivedCount = %d, want 3", response.ArchivedCount)
	}
	if response.Summary != "LLM summary of remaining archive" {
		t.Fatalf("rebuilt summary = %q", response.Summary)
	}
	if len(provider.prompts) != 1 {
		t.Fatalf("summary provider calls = %d, want 1", len(provider.prompts))
	}
	prompt := provider.prompts[0]
	if !strings.HasPrefix(prompt, "Provide a concise summary of this conversation segment, preserving core context and key points.\n") ||
		!strings.Contains(prompt, "\nCONVERSATION:\nuser: keep archived user\nassistant: keep archived answer\n") ||
		strings.Contains(prompt, "deleted archived answer") ||
		strings.Contains(prompt, "stale summary") ||
		strings.Contains(prompt, "Existing context:") {
		t.Fatalf("unexpected summary prompt = %q", prompt)
	}
	if len(provider.models) != 1 || provider.models[0] != "summary-model" {
		t.Fatalf("summary models = %#v", provider.models)
	}
	if got := provider.options[0]["temperature"]; got != 0.3 {
		t.Fatalf("summary temperature = %#v, want 0.3", got)
	}
	if got := provider.options[0]["prompt_cache_key"]; got != "main" {
		t.Fatalf("summary prompt_cache_key = %#v, want main", got)
	}

	remainingArchive, err := store.ReadArchivedMessages(nil, sessionKey)
	if err != nil {
		t.Fatalf("ReadArchivedMessages() error = %v", err)
	}
	if len(remainingArchive) != 3 {
		t.Fatalf("len(remainingArchive) = %d, want 3", len(remainingArchive))
	}
}

func TestHandleDeleteMessageSeries_SummaryProviderFailureUsesCompressionFallback(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	sessionID := "delete-archive-summary-fallback"
	sessionKey := legacyPicoSessionPrefix + sessionID
	if err := store.AddFullMessage(nil, sessionKey, providers.Message{Role: "user", Content: "active"}); err != nil {
		t.Fatalf("AddFullMessage() error = %v", err)
	}
	if err := store.ArchiveMessages(nil, sessionKey, []providers.Message{
		{Role: "user", Content: "remaining archived user"},
		{Role: "assistant", Content: "remaining archived answer"},
		{Role: "assistant", Content: "delete this archived answer"},
	}); err != nil {
		t.Fatalf("ArchiveMessages() error = %v", err)
	}
	if err := store.SetSummary(nil, sessionKey, "stale summary"); err != nil {
		t.Fatalf("SetSummary() error = %v", err)
	}

	h := NewHandler(configPath)
	h.contextSummaryProvider = func(_ *config.Config) (providers.LLMProvider, string, error) {
		return nil, "", errors.New("provider unavailable")
	}
	rec, response := deleteMessageSeriesRequestWithHandler(t, h, sessionID, 2)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.HasPrefix(response.Summary, "Conversation summary: ") ||
		!strings.Contains(response.Summary, "remaining archived answer") ||
		strings.Contains(response.Summary, "delete this archived answer") ||
		strings.Contains(response.Summary, "stale summary") {
		t.Fatalf("fallback summary = %q", response.Summary)
	}
}

func TestHandleDeleteMessageSeries_AllowsEmptyJSONLSession(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	sessionID := "delete-only-message"
	sessionKey := legacyPicoSessionPrefix + sessionID
	if err := store.AddFullMessage(nil, sessionKey, providers.Message{Role: "user", Content: "only"}); err != nil {
		t.Fatalf("AddFullMessage() error = %v", err)
	}

	rec, response := deleteMessageSeriesRequest(t, configPath, sessionID, 0)
	if rec.Code != http.StatusOK || len(response.Messages) != 0 {
		t.Fatalf("delete status=%d messages=%#v body=%s", rec.Code, response.Messages, rec.Body.String())
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	getRec := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID, nil)
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("empty session GET status = %d, want %d, body=%s", getRec.Code, http.StatusOK, getRec.Body.String())
	}
}

func TestHandleDeleteMessageSeries_LegacyJSON(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	manager := session.NewSessionManager(dir)
	sessionID := "delete-legacy-message"
	sessionKey := legacyPicoSessionPrefix + sessionID
	manager.AddMessage(sessionKey, "user", "legacy user")
	manager.AddMessage(sessionKey, "assistant", "legacy answer")
	if err := manager.Save(sessionKey); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	rec, response := deleteMessageSeriesRequest(t, configPath, sessionID, 1)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(response.Messages) != 1 || response.Messages[0].Content != "legacy user" {
		t.Fatalf("response.Messages = %#v", response.Messages)
	}

	path := filepath.Join(dir, sanitizeSessionKey(sessionKey)+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var stored sessionFile
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("Unmarshal(stored) error = %v", err)
	}
	if len(stored.Messages) != 1 || stored.Messages[0].Content != "legacy user" {
		t.Fatalf("stored.Messages = %#v", stored.Messages)
	}
}
