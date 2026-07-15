package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func stringListPointer(values ...string) *[]string {
	result := append([]string(nil), values...)
	return &result
}

func setupAgentPresetAPI(t *testing.T) (string, func()) {
	t.Helper()

	configPath, cleanup := setupOAuthTestEnv(t)
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		cleanup()
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ModelList = append(cfg.ModelList,
		&config.ModelConfig{
			ModelName: "preset-primary",
			Model:     "openai/gpt-4.1",
			APIKeys:   config.SimpleSecureStrings("sk-primary"),
		},
		&config.ModelConfig{
			ModelName: "preset-fallback",
			Model:     "openai/gpt-4.1-mini",
			APIKeys:   config.SimpleSecureStrings("sk-fallback"),
		},
		&config.ModelConfig{
			ModelName: "frontmatter-model",
			Model:     "openai/gpt-4.1-nano",
			APIKeys:   config.SimpleSecureStrings("sk-frontmatter"),
		},
	)
	cfg.AgentPresets = map[string]config.AgentPresetConfig{
		"coding": {
			Model: &config.AgentModelConfig{
				Primary:   "preset-primary",
				Fallbacks: []string{"preset-fallback"},
			},
			Tools: stringListPointer("read_file", "edit_file"),
		},
		"research": {
			Skills: stringListPointer("web-research"),
		},
	}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		cleanup()
		t.Fatalf("SaveConfig() error = %v", err)
	}
	return configPath, cleanup
}

func agentPresetTestMux(configPath string) *http.ServeMux {
	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

func putSessionAgentPreset(
	t *testing.T,
	mux *http.ServeMux,
	sessionID string,
	presetName string,
) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(setSessionAgentPresetRequest{AgentPreset: presetName})
	if err != nil {
		t.Fatalf("Marshal(request) error = %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/sessions/"+sessionID+"/preset",
		bytes.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHandleListAgentPresets(t *testing.T) {
	configPath, cleanup := setupAgentPresetAPI(t)
	defer cleanup()

	mux := agentPresetTestMux(configPath)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agent-presets?session_id=catalog", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response agentPresetCatalogResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if response.DefaultModel != "custom-default" {
		t.Fatalf("DefaultModel = %q, want %q", response.DefaultModel, "custom-default")
	}
	if len(response.Presets) != 2 {
		t.Fatalf("len(Presets) = %d, want 2", len(response.Presets))
	}
	if got := response.Presets[0]; got.Name != "coding" ||
		got.EffectiveModel != "preset-primary" ||
		!got.ModelOverridden ||
		!reflect.DeepEqual(got.Fallbacks, []string{"preset-fallback"}) {
		t.Fatalf("Presets[0] = %#v, want coding preset model and fallback", got)
	}
	if got := response.Presets[1]; got.Name != "research" ||
		got.EffectiveModel != "custom-default" || got.ModelOverridden {
		t.Fatalf("Presets[1] = %#v, want inherited default model", got)
	}
}

func TestHandleListAgentPresets_UsesAgentFrontmatterModel(t *testing.T) {
	configPath, cleanup := setupAgentPresetAPI(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if err := os.MkdirAll(cfg.WorkspacePath(), 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) error = %v", err)
	}
	agentDefinition := []byte("---\nmodel: frontmatter-model\n---\n# Main agent\n")
	if err := os.WriteFile(
		filepath.Join(cfg.WorkspacePath(), "AGENT.md"),
		agentDefinition,
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(AGENT.md) error = %v", err)
	}

	mux := agentPresetTestMux(configPath)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/agent-presets?session_id=frontmatter", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response agentPresetCatalogResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if response.DefaultModel != "frontmatter-model" {
		t.Fatalf("DefaultModel = %q, want %q", response.DefaultModel, "frontmatter-model")
	}
	if got := response.Presets[1]; got.Name != "research" ||
		got.EffectiveModel != "frontmatter-model" {
		t.Fatalf("inherited preset = %#v, want frontmatter model", got)
	}
}

func TestHandleSetSessionAgentPreset_NewSessionAndReset(t *testing.T) {
	configPath, cleanup := setupAgentPresetAPI(t)
	defer cleanup()

	const sessionID = "preset-new-session"
	mux := agentPresetTestMux(configPath)
	rec := putSessionAgentPreset(t, mux, sessionID, "coding")
	if rec.Code != http.StatusOK {
		t.Fatalf("set status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var setResponse sessionAgentPresetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &setResponse); err != nil {
		t.Fatalf("Unmarshal(set response) error = %v", err)
	}
	if setResponse.AgentPreset != "coding" || setResponse.EffectiveModel != "preset-primary" {
		t.Fatalf("set response = %#v", setResponse)
	}

	detailRec := httptest.NewRecorder()
	detailReq := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID, nil)
	mux.ServeHTTP(detailRec, detailReq)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want %d, body=%s", detailRec.Code, http.StatusOK, detailRec.Body.String())
	}
	var detail struct {
		Messages       []sessionChatMessage `json:"messages"`
		AgentPreset    string               `json:"agent_preset"`
		EffectiveModel string               `json:"effective_model"`
	}
	if err := json.Unmarshal(detailRec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("Unmarshal(detail) error = %v", err)
	}
	if len(detail.Messages) != 0 || detail.AgentPreset != "coding" ||
		detail.EffectiveModel != "preset-primary" {
		t.Fatalf("detail = %#v", detail)
	}

	resetRec := putSessionAgentPreset(t, mux, sessionID, "default")
	if resetRec.Code != http.StatusOK {
		t.Fatalf("reset status = %d, want %d, body=%s", resetRec.Code, http.StatusOK, resetRec.Body.String())
	}
	var resetResponse sessionAgentPresetResponse
	if err := json.Unmarshal(resetRec.Body.Bytes(), &resetResponse); err != nil {
		t.Fatalf("Unmarshal(reset response) error = %v", err)
	}
	if resetResponse.AgentPreset != "default" || resetResponse.EffectiveModel != "custom-default" {
		t.Fatalf("reset response = %#v", resetResponse)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	_, allocation := picoRouteAllocation(cfg, sessionID)
	store, err := memory.NewJSONLStore(sessionsTestDir(t, configPath))
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	meta, err := store.GetSessionMeta(context.Background(), allocation.SessionKey)
	if err != nil {
		t.Fatalf("GetSessionMeta() error = %v", err)
	}
	if meta.AgentPreset != "" {
		t.Fatalf("stored AgentPreset = %q, want empty default marker", meta.AgentPreset)
	}
}

func TestHandleSetSessionAgentPreset_RejectsUnknownPreset(t *testing.T) {
	configPath, cleanup := setupAgentPresetAPI(t)
	defer cleanup()

	rec := putSessionAgentPreset(
		t,
		agentPresetTestMux(configPath),
		"unknown-preset-session",
		"missing",
	)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleSetSessionAgentPreset_PreservesSessionMetadata(t *testing.T) {
	configPath, cleanup := setupAgentPresetAPI(t)
	defer cleanup()

	const sessionID = "preset-preserve-meta"
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	_, allocation := picoRouteAllocation(cfg, sessionID)
	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	scope, err := json.Marshal(allocation.Scope)
	if err != nil {
		t.Fatalf("Marshal(scope) error = %v", err)
	}
	if err := store.UpsertSessionMeta(
		context.Background(),
		allocation.SessionKey,
		scope,
		allocation.SessionAliases,
	); err != nil {
		t.Fatalf("UpsertSessionMeta() error = %v", err)
	}
	if err := store.AddFullMessage(context.Background(), allocation.SessionKey, providers.Message{
		Role:    "user",
		Content: "preserve this message",
	}); err != nil {
		t.Fatalf("AddFullMessage() error = %v", err)
	}
	if err := store.SetSummary(context.Background(), allocation.SessionKey, "preserve this summary"); err != nil {
		t.Fatalf("SetSummary() error = %v", err)
	}
	before, err := store.GetSessionMeta(context.Background(), allocation.SessionKey)
	if err != nil {
		t.Fatalf("GetSessionMeta(before) error = %v", err)
	}

	rec := putSessionAgentPreset(t, agentPresetTestMux(configPath), sessionID, "research")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	after, err := store.GetSessionMeta(context.Background(), allocation.SessionKey)
	if err != nil {
		t.Fatalf("GetSessionMeta(after) error = %v", err)
	}
	if after.AgentPreset != "research" || after.Summary != before.Summary ||
		after.Count != before.Count || after.Skip != before.Skip ||
		!bytes.Equal(after.Scope, before.Scope) ||
		!reflect.DeepEqual(after.Aliases, before.Aliases) {
		t.Fatalf("metadata changed unexpectedly: before=%#v after=%#v", before, after)
	}
}

func TestHandleForkSession_InheritsAgentPreset(t *testing.T) {
	configPath, cleanup := setupAgentPresetAPI(t)
	defer cleanup()

	const (
		sourceID = "preset-fork-source"
		targetID = "preset-fork-target"
	)
	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	sourceKey := legacyPicoSessionPrefix + sourceID
	if err := store.AddFullMessage(context.Background(), sourceKey, providers.Message{
		Role:    "user",
		Content: "fork from here",
	}); err != nil {
		t.Fatalf("AddFullMessage() error = %v", err)
	}
	if err := store.SetSessionAgentPreset(context.Background(), sourceKey, "coding"); err != nil {
		t.Fatalf("SetSessionAgentPreset() error = %v", err)
	}

	body, err := json.Marshal(forkSessionRequest{
		NewSessionID:    targetID,
		TranscriptIndex: 0,
	})
	if err != nil {
		t.Fatalf("Marshal(request) error = %v", err)
	}
	mux := agentPresetTestMux(configPath)
	forkRec := httptest.NewRecorder()
	forkReq := httptest.NewRequest(
		http.MethodPost,
		"/api/sessions/"+sourceID+"/fork",
		bytes.NewReader(body),
	)
	mux.ServeHTTP(forkRec, forkReq)
	if forkRec.Code != http.StatusOK {
		t.Fatalf("fork status = %d, want %d, body=%s", forkRec.Code, http.StatusOK, forkRec.Body.String())
	}

	detailRec := httptest.NewRecorder()
	detailReq := httptest.NewRequest(http.MethodGet, "/api/sessions/"+targetID, nil)
	mux.ServeHTTP(detailRec, detailReq)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want %d, body=%s", detailRec.Code, http.StatusOK, detailRec.Body.String())
	}
	var detail struct {
		AgentPreset    string `json:"agent_preset"`
		EffectiveModel string `json:"effective_model"`
	}
	if err := json.Unmarshal(detailRec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("Unmarshal(detail) error = %v", err)
	}
	if detail.AgentPreset != "coding" || detail.EffectiveModel != "preset-primary" {
		t.Fatalf("forked detail = %#v", detail)
	}
}
