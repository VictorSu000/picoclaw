package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const DefaultAgentPresetName = "default"

var agentPresetNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// AgentPresetConfig is a named, request-time override applied on top of the
// routed agent. Nil list pointers inherit the agent's existing behavior;
// non-nil empty lists explicitly disable that capability.
type AgentPresetConfig struct {
	Model  *AgentModelConfig `json:"model,omitempty"`
	Tools  *[]string         `json:"tools,omitempty"`
	Skills *[]string         `json:"skills,omitempty"`
	MCP    *[]string         `json:"mcp,omitempty"`
}

// EffectiveAgentPreset is the normalized immutable preset attached to one
// turn. The Specified flags preserve the distinction between an omitted list
// and an explicitly empty one.
type EffectiveAgentPreset struct {
	Name            string
	Model           *AgentModelConfig
	Tools           []string
	ToolsSpecified  bool
	Skills          []string
	SkillsSpecified bool
	MCP             []string
	MCPSpecified    bool
}

func (p EffectiveAgentPreset) Enabled() bool {
	return strings.TrimSpace(p.Name) != ""
}

func (c *Config) AgentPresetNames() []string {
	if c == nil || len(c.AgentPresets) == 0 {
		return nil
	}
	names := make([]string, 0, len(c.AgentPresets))
	for name := range c.AgentPresets {
		names = append(names, strings.TrimSpace(name))
	}
	sort.Strings(names)
	return names
}

// ResolveAgentPreset resolves a configured preset name case-insensitively.
// "default" and an empty name mean that no preset override should be used.
func (c *Config) ResolveAgentPreset(name string) (EffectiveAgentPreset, bool, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || name == DefaultAgentPresetName {
		return EffectiveAgentPreset{}, false, nil
	}
	if c == nil {
		return EffectiveAgentPreset{}, false, fmt.Errorf("agent preset %q not found", name)
	}

	var (
		resolvedName string
		preset       AgentPresetConfig
		found        bool
	)
	for candidateName, candidate := range c.AgentPresets {
		trimmedCandidateName := strings.TrimSpace(candidateName)
		if strings.EqualFold(trimmedCandidateName, name) {
			resolvedName = trimmedCandidateName
			preset = candidate
			found = true
			break
		}
	}
	if !found {
		return EffectiveAgentPreset{}, false, fmt.Errorf("agent preset %q not found", name)
	}

	effective := EffectiveAgentPreset{Name: resolvedName}
	if preset.Model != nil {
		model := *preset.Model
		model.Fallbacks = append([]string(nil), preset.Model.Fallbacks...)
		effective.Model = &model
	}
	if preset.Tools != nil {
		effective.ToolsSpecified = true
		effective.Tools = cleanStringList(*preset.Tools)
	}
	if preset.Skills != nil {
		effective.SkillsSpecified = true
		effective.Skills = cleanStringList(*preset.Skills)
	}
	if preset.MCP != nil {
		effective.MCPSpecified = true
		effective.MCP = cleanStringList(*preset.MCP)
	}
	return effective, true, nil
}

func (c *Config) ValidateAgentPresets() error {
	if c == nil || len(c.AgentPresets) == 0 {
		return nil
	}

	seenNames := make(map[string]string, len(c.AgentPresets))
	for rawName, preset := range c.AgentPresets {
		trimmedName := strings.TrimSpace(rawName)
		if trimmedName != rawName {
			return fmt.Errorf("agent_presets.%s: name must not have leading or trailing whitespace", rawName)
		}
		name := strings.ToLower(trimmedName)
		if !agentPresetNamePattern.MatchString(name) {
			return fmt.Errorf("agent_presets.%s: name must match %s", rawName, agentPresetNamePattern.String())
		}
		if name == DefaultAgentPresetName {
			return fmt.Errorf("agent_presets.%s: %q is reserved for the agent's default behavior", rawName, DefaultAgentPresetName)
		}
		if previous, exists := seenNames[name]; exists {
			return fmt.Errorf("agent_presets contains case-insensitive duplicate names %q and %q", previous, rawName)
		}
		seenNames[name] = rawName

		if err := c.validateAgentPreset(rawName, preset); err != nil {
			return err
		}
	}
	return nil
}

// ValidateChannelDefaultPresets verifies that each configured channel default
// refers to an existing agent preset. Empty and "default" mean agent defaults.
func (c *Config) ValidateChannelDefaultPresets() error {
	if c == nil {
		return nil
	}
	for channelName, channel := range c.Channels {
		if channel == nil {
			continue
		}
		name := strings.TrimSpace(channel.DefaultPreset)
		if name == "" || strings.EqualFold(name, DefaultAgentPresetName) {
			continue
		}
		if _, found, err := c.ResolveAgentPreset(name); err != nil || !found {
			if err != nil {
				return fmt.Errorf("channel_list.%s.default_preset: %w", channelName, err)
			}
			return fmt.Errorf("channel_list.%s.default_preset: agent preset %q not found", channelName, name)
		}
	}
	return nil
}

func (c *Config) validateAgentPreset(name string, preset AgentPresetConfig) error {
	if preset.Model != nil {
		primary := strings.TrimSpace(preset.Model.Primary)
		if primary == "" {
			return fmt.Errorf("agent_presets.%s.model.primary is required", name)
		}
		if len(c.findMatches(primary)) == 0 {
			return fmt.Errorf("agent_presets.%s.model.primary references unknown model %q", name, primary)
		}
		for i, fallback := range preset.Model.Fallbacks {
			fallback = strings.TrimSpace(fallback)
			if fallback == "" {
				return fmt.Errorf("agent_presets.%s.model.fallbacks[%d] must not be empty", name, i)
			}
			if len(c.findMatches(fallback)) == 0 {
				return fmt.Errorf("agent_presets.%s.model.fallbacks[%d] references unknown model %q", name, i, fallback)
			}
		}
	}

	if err := validateAgentPresetList(name, "tools", preset.Tools); err != nil {
		return err
	}
	if err := validateAgentPresetList(name, "skills", preset.Skills); err != nil {
		return err
	}
	if err := validateAgentPresetList(name, "mcp", preset.MCP); err != nil {
		return err
	}

	if preset.MCP != nil {
		knownServers := make(map[string]struct{}, len(c.Tools.MCP.Servers))
		for serverName := range c.Tools.MCP.Servers {
			knownServers[strings.ToLower(strings.TrimSpace(serverName))] = struct{}{}
		}
		for i, serverName := range *preset.MCP {
			if _, ok := knownServers[strings.ToLower(strings.TrimSpace(serverName))]; !ok {
				return fmt.Errorf("agent_presets.%s.mcp[%d] references unknown MCP server %q", name, i, serverName)
			}
		}
	}
	return nil
}

func validateAgentPresetList(presetName, field string, values *[]string) error {
	if values == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(*values))
	for i, value := range *values {
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("agent_presets.%s.%s[%d] must not be empty", presetName, field, i)
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("agent_presets.%s.%s contains duplicate value %q", presetName, field, value)
		}
		seen[key] = struct{}{}
	}
	return nil
}
