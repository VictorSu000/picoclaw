package agent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/tools"
)

func (al *AgentLoop) resolveAgentPresetOptions(
	agent *AgentInstance,
	opts processOptions,
) (processOptions, error) {
	if opts.AgentPresetResolved {
		return opts, nil
	}
	opts.AgentPresetResolved = true
	if agent == nil || strings.TrimSpace(opts.Dispatch.SessionKey) == "" {
		return opts, nil
	}

	store, ok := agent.Sessions.(session.AgentPresetSessionStore)
	if !ok {
		return opts, nil
	}
	presetName := strings.TrimSpace(store.GetAgentPreset(opts.Dispatch.SessionKey))
	if presetName == "" {
		return opts, nil
	}

	preset, found, err := al.GetConfig().ResolveAgentPreset(presetName)
	if err != nil || !found {
		_ = store.SetAgentPreset(opts.Dispatch.SessionKey, "")
		if err == nil {
			err = fmt.Errorf("agent preset %q not found", presetName)
		}
		return opts, fmt.Errorf("selected agent preset is no longer available and was reset: %w", err)
	}
	if err := validateAgentPresetForAgent(al.GetConfig(), agent, preset); err != nil {
		_ = store.SetAgentPreset(opts.Dispatch.SessionKey, "")
		return opts, fmt.Errorf("selected agent preset is no longer valid and was reset: %w", err)
	}
	opts.AgentPreset = preset
	return opts, nil
}

func applyAgentPresetToOptions(
	cfg *config.Config,
	agent *AgentInstance,
	opts *processOptions,
	presetName string,
) error {
	if opts == nil {
		return fmt.Errorf("process options are not available")
	}
	preset, found, err := cfg.ResolveAgentPreset(presetName)
	if err != nil {
		return err
	}
	if !found {
		opts.AgentPreset = config.EffectiveAgentPreset{}
		opts.AgentPresetResolved = true
		return nil
	}
	if err := validateAgentPresetForAgent(cfg, agent, preset); err != nil {
		return err
	}
	opts.AgentPreset = preset
	opts.AgentPresetResolved = true
	return nil
}

func validateAgentPresetForAgent(
	cfg *config.Config,
	agent *AgentInstance,
	preset config.EffectiveAgentPreset,
) error {
	if agent == nil {
		return fmt.Errorf("agent is not available")
	}
	if preset.Model != nil {
		candidates := agent.PresetCandidates[strings.ToLower(preset.Name)]
		if len(candidates) == 0 {
			return fmt.Errorf("agent preset %q model did not resolve to any provider candidates", preset.Name)
		}
		for _, candidate := range candidates {
			key := providers.ModelKey(candidate.Provider, candidate.Model)
			if provider := agent.CandidateProviders[key]; provider == nil {
				return fmt.Errorf(
					"agent preset %q model %q could not initialize its provider",
					preset.Name,
					candidate.DisplayName,
				)
			}
		}
	}
	if preset.ToolsSpecified {
		for _, requested := range preset.Tools {
			name, ok := resolveRegisteredToolName(agent.Tools, requested)
			if !ok {
				return fmt.Errorf("agent preset %q requires unavailable tool %q", preset.Name, requested)
			}
			if _, isMCP := agent.Tools.MCPServerForTool(name); isMCP {
				return fmt.Errorf("agent preset %q lists MCP tool %q in tools; select its server through mcp instead", preset.Name, requested)
			}
		}
	}
	if preset.SkillsSpecified {
		for _, requested := range preset.Skills {
			if agent.ContextBuilder == nil {
				return fmt.Errorf("agent preset %q requires skill %q but skills are unavailable", preset.Name, requested)
			}
			if _, ok := agent.ContextBuilder.ResolveSkillName(requested); !ok {
				return fmt.Errorf("agent preset %q requires unavailable skill %q", preset.Name, requested)
			}
		}
	}
	if preset.MCPSpecified {
		if cfg == nil || !cfg.Tools.MCP.Enabled {
			if len(preset.MCP) == 0 {
				return nil
			}
			return fmt.Errorf("agent preset %q requires MCP but MCP is disabled", preset.Name)
		}
		for _, requested := range preset.MCP {
			serverName, serverCfg, ok := resolveMCPServerConfig(cfg, requested)
			if !ok || !serverCfg.Enabled {
				return fmt.Errorf("agent preset %q requires unavailable MCP server %q", preset.Name, requested)
			}
			if !agent.AllowsMCPServer(serverName) {
				return fmt.Errorf("agent preset %q cannot use MCP server %q because the agent does not allow it", preset.Name, requested)
			}
		}
	}
	return nil
}

func resolveRegisteredToolName(registry *tools.ToolRegistry, requested string) (string, bool) {
	if registry == nil {
		return "", false
	}
	for _, name := range registry.List() {
		if strings.EqualFold(name, strings.TrimSpace(requested)) {
			return name, true
		}
	}
	return "", false
}

func resolveMCPServerConfig(
	cfg *config.Config,
	requested string,
) (string, config.MCPServerConfig, bool) {
	if cfg == nil {
		return "", config.MCPServerConfig{}, false
	}
	for name, serverCfg := range cfg.Tools.MCP.Servers {
		if strings.EqualFold(name, strings.TrimSpace(requested)) {
			return name, serverCfg, true
		}
	}
	return "", config.MCPServerConfig{}, false
}

func setSessionAgentPreset(agent *AgentInstance, sessionKey, presetName string) error {
	if agent == nil || agent.Sessions == nil {
		return fmt.Errorf("session store is unavailable")
	}
	store, ok := agent.Sessions.(session.AgentPresetSessionStore)
	if !ok {
		return fmt.Errorf("session store does not support agent presets")
	}
	return store.SetAgentPreset(sessionKey, strings.TrimSpace(presetName))
}

func agentPresetSkillAllowed(preset config.EffectiveAgentPreset, name string) bool {
	if !preset.Enabled() || !preset.SkillsSpecified {
		return true
	}
	allowed := cleanAllowedSet(preset.Skills)
	_, ok := allowed[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

func agentPresetSkillNames(
	agent *AgentInstance,
	preset config.EffectiveAgentPreset,
) []string {
	if agent == nil || agent.ContextBuilder == nil {
		return nil
	}
	if !preset.Enabled() || !preset.SkillsSpecified {
		return agent.ContextBuilder.ListSkillNames()
	}
	names := make([]string, 0, len(preset.Skills))
	for _, requested := range preset.Skills {
		if canonical, ok := agent.ContextBuilder.ResolveSkillName(requested); ok {
			names = append(names, canonical)
		}
	}
	return names
}

func agentPresetToolAllowed(
	agent *AgentInstance,
	preset config.EffectiveAgentPreset,
	name string,
) bool {
	if !preset.Enabled() {
		return true
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if agent != nil && agent.Tools != nil {
		if serverName, isMCP := agent.Tools.MCPServerForTool(name); isMCP {
			if !preset.MCPSpecified {
				return true
			}
			_, ok := cleanAllowedSet(preset.MCP)[strings.ToLower(serverName)]
			return ok
		}
	}
	if isToolDiscoveryName(name) {
		return !preset.MCPSpecified || len(preset.MCP) > 0
	}
	if !preset.ToolsSpecified {
		return true
	}
	_, ok := cleanAllowedSet(preset.Tools)[strings.ToLower(name)]
	return ok
}

func isToolDiscoveryName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case tools.BM25SearchToolName, tools.RegexSearchToolName:
		return true
	default:
		return false
	}
}

func toolAllowedForTurn(
	agent *AgentInstance,
	turnPolicy config.EffectiveTurnProfile,
	preset config.EffectiveAgentPreset,
	name string,
) bool {
	return turnProfileToolAllowed(turnPolicy, name) && agentPresetToolAllowed(agent, preset, name)
}

func filterToolsForTurn(
	agent *AgentInstance,
	defs []providers.ToolDefinition,
	turnPolicy config.EffectiveTurnProfile,
	preset config.EffectiveAgentPreset,
) []providers.ToolDefinition {
	defs = filterToolsByTurnProfile(defs, turnPolicy)
	if !preset.Enabled() || (!preset.ToolsSpecified && !preset.MCPSpecified) {
		return defs
	}
	filtered := make([]providers.ToolDefinition, 0, len(defs))
	for _, def := range defs {
		if agentPresetToolAllowed(agent, preset, def.Function.Name) {
			filtered = append(filtered, def)
		}
	}
	return filtered
}

func allowedToolNamesForTurn(
	agent *AgentInstance,
	turnPolicy config.EffectiveTurnProfile,
	preset config.EffectiveAgentPreset,
) ([]string, bool) {
	restricted := turnPolicy.Enabled &&
		(turnPolicy.ToolsMode == config.TurnProfileModeOff || turnPolicy.ToolsMode == config.TurnProfileModeCustom)
	restricted = restricted || (preset.Enabled() && (preset.ToolsSpecified || preset.MCPSpecified))
	if !restricted || agent == nil || agent.Tools == nil {
		return nil, restricted
	}

	names := make([]string, 0)
	for _, name := range agent.Tools.List() {
		if toolAllowedForTurn(agent, turnPolicy, preset, name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, true
}

func allowedSkillsForTurn(
	turnPolicy config.EffectiveTurnProfile,
	preset config.EffectiveAgentPreset,
) ([]string, bool) {
	if turnProfileSkillsOff(turnPolicy) {
		return nil, true
	}
	if !preset.Enabled() || !preset.SkillsSpecified {
		if turnProfileCustomSkills(turnPolicy) {
			return append([]string(nil), turnPolicy.AllowedSkills...), true
		}
		return nil, false
	}
	allowed := append([]string(nil), preset.Skills...)
	if turnProfileCustomSkills(turnPolicy) {
		allowed = filterNamesByTurnProfile(allowed, turnPolicy.AllowedSkills)
	}
	return allowed, true
}
