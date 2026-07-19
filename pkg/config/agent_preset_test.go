package config

import (
	"encoding/json"
	"testing"
)

func stringSlicePtr(values ...string) *[]string {
	copyValues := append([]string(nil), values...)
	return &copyValues
}

func presetTestConfig() *Config {
	return &Config{
		ModelList: []*ModelConfig{
			{ModelName: "main", Model: "openai/main"},
			{ModelName: "fallback", Model: "anthropic/fallback"},
		},
		Tools: ToolsConfig{MCP: MCPConfig{Servers: map[string]MCPServerConfig{
			"github": {Enabled: true},
		}}},
	}
}

func TestAgentPresetConfigDistinguishesOmittedAndEmptyLists(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{
		"agent_presets": {
			"inherit": {},
			"disabled": {"tools": [], "skills": [], "mcp": []}
		}
	}`), &cfg); err != nil {
		t.Fatal(err)
	}

	inherit, found, err := cfg.ResolveAgentPreset("inherit")
	if err != nil || !found {
		t.Fatalf("ResolveAgentPreset(inherit) = (%+v, %v, %v)", inherit, found, err)
	}
	if inherit.ToolsSpecified || inherit.SkillsSpecified || inherit.MCPSpecified {
		t.Fatalf("omitted lists were marked specified: %+v", inherit)
	}

	disabled, found, err := cfg.ResolveAgentPreset("disabled")
	if err != nil || !found {
		t.Fatalf("ResolveAgentPreset(disabled) = (%+v, %v, %v)", disabled, found, err)
	}
	if !disabled.ToolsSpecified || !disabled.SkillsSpecified || !disabled.MCPSpecified {
		t.Fatalf("empty lists were not marked specified: %+v", disabled)
	}
	if len(disabled.Tools) != 0 || len(disabled.Skills) != 0 || len(disabled.MCP) != 0 {
		t.Fatalf("explicit empty lists were not preserved: %+v", disabled)
	}

	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var roundTripped Config
	if err := json.Unmarshal(encoded, &roundTripped); err != nil {
		t.Fatal(err)
	}
	disabled, found, err = roundTripped.ResolveAgentPreset("disabled")
	if err != nil || !found || !disabled.ToolsSpecified || !disabled.SkillsSpecified || !disabled.MCPSpecified {
		t.Fatalf("empty lists were lost after JSON round trip: (%+v, %v, %v)", disabled, found, err)
	}
}

func TestAgentPresetModelAcceptsStringAndObject(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{
		"agent_presets": {
			"simple": {"model": "main"},
			"fallbacks": {"model": {"primary": "main", "fallbacks": ["fallback"]}}
		}
	}`), &cfg); err != nil {
		t.Fatal(err)
	}

	simple, _, _ := cfg.ResolveAgentPreset("simple")
	if simple.Model == nil || simple.Model.Primary != "main" || len(simple.Model.Fallbacks) != 0 {
		t.Fatalf("simple model = %+v", simple.Model)
	}
	withFallback, _, _ := cfg.ResolveAgentPreset("fallbacks")
	if withFallback.Model == nil || len(withFallback.Model.Fallbacks) != 1 ||
		withFallback.Model.Fallbacks[0] != "fallback" {
		t.Fatalf("fallback model = %+v", withFallback.Model)
	}
}

func TestValidateAgentPresets(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		cfg := presetTestConfig()
		cfg.AgentPresets = map[string]AgentPresetConfig{
			"coding": {
				Model:  &AgentModelConfig{Primary: "main", Fallbacks: []string{"fallback"}},
				Tools:  stringSlicePtr("read_file"),
				Skills: stringSlicePtr("code-review"),
				MCP:    stringSlicePtr("github"),
			},
		}
		if err := cfg.ValidateAgentPresets(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("reserved default", func(t *testing.T) {
		cfg := presetTestConfig()
		cfg.AgentPresets = map[string]AgentPresetConfig{"default": {}}
		if err := cfg.ValidateAgentPresets(); err == nil {
			t.Fatal("expected reserved default error")
		}
	})

	t.Run("name whitespace", func(t *testing.T) {
		cfg := presetTestConfig()
		cfg.AgentPresets = map[string]AgentPresetConfig{" coding ": {}}
		if err := cfg.ValidateAgentPresets(); err == nil {
			t.Fatal("expected preset name whitespace error")
		}
	})

	t.Run("unknown model", func(t *testing.T) {
		cfg := presetTestConfig()
		cfg.AgentPresets = map[string]AgentPresetConfig{
			"bad": {Model: &AgentModelConfig{Primary: "missing"}},
		}
		if err := cfg.ValidateAgentPresets(); err == nil {
			t.Fatal("expected unknown model error")
		}
	})

	t.Run("unknown mcp server", func(t *testing.T) {
		cfg := presetTestConfig()
		cfg.AgentPresets = map[string]AgentPresetConfig{
			"bad": {MCP: stringSlicePtr("missing")},
		}
		if err := cfg.ValidateAgentPresets(); err == nil {
			t.Fatal("expected unknown MCP server error")
		}
	})
}

func TestValidateChannelDefaultPresets(t *testing.T) {
	cfg := presetTestConfig()
	cfg.AgentPresets = map[string]AgentPresetConfig{"coding": {}}
	cfg.Channels = ChannelsConfig{
		"telegram": {Type: ChannelTelegram, DefaultPreset: "Coding"},
		"discord":  {Type: ChannelDiscord, DefaultPreset: DefaultAgentPresetName},
	}
	if err := cfg.ValidateChannelDefaultPresets(); err != nil {
		t.Fatalf("ValidateChannelDefaultPresets() error = %v", err)
	}

	cfg.Channels["telegram"].DefaultPreset = "missing"
	if err := cfg.ValidateChannelDefaultPresets(); err == nil {
		t.Fatal("expected unknown channel default preset error")
	}
}
