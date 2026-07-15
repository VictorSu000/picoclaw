package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/routing"
	"github.com/sipeed/picoclaw/pkg/session"
)

type agentPresetListItem struct {
	Name            string   `json:"name"`
	EffectiveModel  string   `json:"effective_model"`
	Fallbacks       []string `json:"fallbacks,omitempty"`
	ModelOverridden bool     `json:"model_overridden"`
}

type agentPresetCatalogResponse struct {
	DefaultModel string                `json:"default_model"`
	Presets      []agentPresetListItem `json:"presets"`
}

type setSessionAgentPresetRequest struct {
	AgentPreset string `json:"agent_preset"`
}

type sessionAgentPresetResponse struct {
	AgentPreset    string   `json:"agent_preset"`
	EffectiveModel string   `json:"effective_model"`
	Fallbacks      []string `json:"fallbacks,omitempty"`
}

func agentPresetResponseName(name string) string {
	if strings.TrimSpace(name) == "" {
		return config.DefaultAgentPresetName
	}
	return name
}

func (h *Handler) registerAgentPresetRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/agent-presets", h.handleListAgentPresets)
	mux.HandleFunc("PUT /api/sessions/{id}/preset", h.handleSetSessionAgentPreset)
}

func picoRouteAllocation(cfg *config.Config, sessionID string) (routing.ResolvedRoute, session.Allocation) {
	inbound := bus.NormalizeInboundMessage(bus.InboundMessage{Context: bus.InboundContext{
		Channel:  "pico",
		ChatID:   "pico:" + strings.TrimSpace(sessionID),
		ChatType: "direct",
		SenderID: "pico-user",
	}}).Context
	route := routing.NewRouteResolver(cfg).ResolveRoute(inbound)
	allocation := session.AllocateRouteSession(session.AllocationInput{
		AgentID:       route.AgentID,
		Context:       inbound,
		SessionPolicy: route.SessionPolicy,
	})
	return route, allocation
}

func configuredAgentModel(cfg *config.Config, agentID string) (string, []string) {
	if cfg == nil {
		return "", nil
	}

	normalizedID := routing.NormalizeAgentID(agentID)
	var agentCfg *config.AgentConfig
	for i := range cfg.Agents.List {
		if routing.NormalizeAgentID(cfg.Agents.List[i].ID) == normalizedID {
			agentCfg = &cfg.Agents.List[i]
			break
		}
	}

	fallbacks := append([]string(nil), cfg.Agents.Defaults.ModelFallbacks...)
	if agentCfg != nil && agentCfg.Model != nil && agentCfg.Model.Fallbacks != nil {
		fallbacks = append([]string(nil), agentCfg.Model.Fallbacks...)
	}

	workspace := cfg.WorkspacePath()
	if agentCfg != nil && strings.TrimSpace(agentCfg.Workspace) != "" {
		workspace = expandAgentPresetWorkspace(agentCfg.Workspace)
	} else if agentCfg != nil && !agentCfg.Default &&
		routing.NormalizeAgentID(agentCfg.ID) != routing.DefaultAgentID {
		workspace = filepath.Join(workspace, "..", "workspace-"+normalizedID)
	}
	definition := agent.LoadAgentDefinition(workspace)
	if definition.Agent != nil {
		if primary := strings.TrimSpace(definition.Agent.Frontmatter.Model); primary != "" {
			return primary, fallbacks
		}
	}
	if agentCfg != nil && agentCfg.Model != nil {
		if primary := strings.TrimSpace(agentCfg.Model.Primary); primary != "" {
			return primary, fallbacks
		}
	}
	return strings.TrimSpace(cfg.Agents.Defaults.GetModelName()), fallbacks
}

func expandAgentPresetWorkspace(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if strings.HasPrefix(workspace, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			if len(workspace) > 1 && workspace[1] == '/' {
				return home + workspace[1:]
			}
			return home
		}
	}
	return workspace
}

func effectiveModelForAgentPreset(
	cfg *config.Config,
	agentID string,
	presetName string,
) (string, []string, error) {
	baseModel, baseFallbacks := configuredAgentModel(cfg, agentID)
	preset, found, err := cfg.ResolveAgentPreset(presetName)
	if err != nil {
		return "", nil, err
	}
	if !found || preset.Model == nil {
		return baseModel, baseFallbacks, nil
	}
	return strings.TrimSpace(preset.Model.Primary),
		append([]string(nil), preset.Model.Fallbacks...), nil
}

func (h *Handler) handleListAgentPresets(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	route, _ := picoRouteAllocation(cfg, r.URL.Query().Get("session_id"))
	defaultModel, _, _ := effectiveModelForAgentPreset(cfg, route.AgentID, config.DefaultAgentPresetName)
	items := make([]agentPresetListItem, 0, len(cfg.AgentPresets))
	for _, name := range cfg.AgentPresetNames() {
		preset, found, resolveErr := cfg.ResolveAgentPreset(name)
		if resolveErr != nil || !found {
			continue
		}
		model, fallbacks, modelErr := effectiveModelForAgentPreset(cfg, route.AgentID, name)
		if modelErr != nil {
			continue
		}
		items = append(items, agentPresetListItem{
			Name:            preset.Name,
			EffectiveModel:  model,
			Fallbacks:       fallbacks,
			ModelOverridden: preset.Model != nil,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(agentPresetCatalogResponse{
		DefaultModel: defaultModel,
		Presets:      items,
	})
}

func (h *Handler) handleSetSessionAgentPreset(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	if sessionID == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	var req setSessionAgentPresetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	preset, found, err := cfg.ResolveAgentPreset(req.AgentPreset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	storedName := ""
	if found {
		storedName = preset.Name
	}

	dir, err := h.sessionsDir()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "failed to create sessions directory", http.StatusInternalServerError)
		return
	}

	route, allocation := picoRouteAllocation(cfg, sessionID)
	sessionKey := allocation.SessionKey
	ref, findErr := h.findPicoJSONLSession(dir, sessionID)
	if findErr == nil {
		sessionKey = ref.Key
	} else if !errors.Is(findErr, os.ErrNotExist) {
		http.Error(w, "failed to find session", http.StatusInternalServerError)
		return
	} else if _, legacyErr := h.findLegacyPicoSession(dir, sessionID); legacyErr == nil {
		http.Error(w, "agent presets are unavailable for legacy sessions", http.StatusConflict)
		return
	}

	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		http.Error(w, "failed to open session store", http.StatusInternalServerError)
		return
	}
	defer store.Close()

	if errors.Is(findErr, os.ErrNotExist) {
		scopeData, marshalErr := json.Marshal(allocation.Scope)
		if marshalErr != nil {
			http.Error(w, "failed to encode session scope", http.StatusInternalServerError)
			return
		}
		if err := store.UpsertSessionMeta(
			context.Background(),
			sessionKey,
			scopeData,
			allocation.SessionAliases,
		); err != nil {
			http.Error(w, "failed to initialize session metadata", http.StatusInternalServerError)
			return
		}
	}
	if err := store.SetSessionAgentPreset(context.Background(), sessionKey, storedName); err != nil {
		http.Error(w, "failed to save agent preset", http.StatusInternalServerError)
		return
	}

	effectiveModel, fallbacks, err := effectiveModelForAgentPreset(cfg, route.AgentID, storedName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionAgentPresetResponse{
		AgentPreset:    agentPresetResponseName(storedName),
		EffectiveModel: effectiveModel,
		Fallbacks:      fallbacks,
	})
}
