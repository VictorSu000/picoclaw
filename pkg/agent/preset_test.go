package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type presetTestTool struct {
	name   string
	server string
}

func (t *presetTestTool) Name() string        { return t.name }
func (t *presetTestTool) Description() string { return t.name }
func (t *presetTestTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (t *presetTestTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	return tools.SilentResult("ok")
}
func (t *presetTestTool) MCPServerName() string { return t.server }

func TestAgentPresetSkillsAreCatalogOnlyUntilUse(t *testing.T) {
	workspace := t.TempDir()
	skillDir := filepath.Join(workspace, "skills", "research")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: research\ndescription: Research summary\n---\n# Research\n\nUNIQUE_FULL_SKILL_BODY"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	agent := &AgentInstance{
		SkillsFilter:   []string{"research"},
		ContextBuilder: NewContextBuilder(workspace),
		Tools:          tools.NewToolRegistry(),
	}
	opts := processOptions{
		AgentPreset: config.EffectiveAgentPreset{
			Name:            "research-only",
			Skills:          []string{"research"},
			SkillsSpecified: true,
		},
		AgentPresetResolved: true,
	}

	if active := activeSkillNames(agent, opts); len(active) != 0 {
		t.Fatalf("preset skills were auto-activated: %v", active)
	}
	promptReq := promptBuildRequestForProcessOptions(agent, opts, nil, "", "hello", nil)
	messages := agent.ContextBuilder.BuildMessagesFromPrompt(promptReq)
	if len(messages) == 0 {
		t.Fatal("no prompt messages built")
	}
	systemPrompt := messages[0].Content
	if !strings.Contains(systemPrompt, "Research summary") {
		t.Fatalf("skill summary missing:\n%s", systemPrompt)
	}
	if strings.Contains(systemPrompt, "UNIQUE_FULL_SKILL_BODY") || strings.Contains(systemPrompt, "# Active Skills") {
		t.Fatalf("skill body was injected without /use:\n%s", systemPrompt)
	}

	opts.ForcedSkills = []string{"research"}
	promptReq = promptBuildRequestForProcessOptions(agent, opts, nil, "", "hello", nil)
	messages = agent.ContextBuilder.BuildMessagesFromPrompt(promptReq)
	if !strings.Contains(messages[0].Content, "UNIQUE_FULL_SKILL_BODY") ||
		!strings.Contains(messages[0].Content, "# Active Skills") {
		t.Fatalf("explicitly activated skill body missing:\n%s", messages[0].Content)
	}
}

func TestAgentPresetSeparatesOrdinaryToolsAndMCPServers(t *testing.T) {
	registry := tools.NewToolRegistry()
	registry.Register(&presetTestTool{name: "read_file"})
	registry.Register(&presetTestTool{name: "exec"})
	registry.Register(&presetTestTool{name: "mcp_github_search", server: "github"})
	registry.Register(&presetTestTool{name: "mcp_gitlab_search", server: "gitlab"})
	agent := &AgentInstance{Tools: registry}
	preset := config.EffectiveAgentPreset{
		Name:           "safe",
		Tools:          []string{"read_file"},
		ToolsSpecified: true,
		MCP:            []string{"github"},
		MCPSpecified:   true,
	}

	defs := filterToolsForTurn(agent, registry.ToProviderDefs(), config.EffectiveTurnProfile{}, preset)
	got := make(map[string]bool)
	for _, def := range defs {
		got[def.Function.Name] = true
	}
	if !got["read_file"] || !got["mcp_github_search"] {
		t.Fatalf("allowed tools missing: %v", got)
	}
	if got["exec"] || got["mcp_gitlab_search"] {
		t.Fatalf("blocked tools leaked: %v", got)
	}
	if toolAllowedForTurn(agent, config.EffectiveTurnProfile{}, preset, "exec") {
		t.Fatal("exec unexpectedly allowed")
	}
	if toolAllowedForTurn(agent, config.EffectiveTurnProfile{}, preset, "mcp_gitlab_search") {
		t.Fatal("gitlab MCP tool unexpectedly allowed")
	}
}

func TestAgentPresetFiltersMCPServerPrompt(t *testing.T) {
	contributor := mcpServerPromptContributor{serverName: "github", toolCount: 2}
	parts, err := contributor.ContributePrompt(context.Background(), PromptBuildRequest{
		AllowedMCPServers:  []string{"gitlab"},
		RestrictMCPServers: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 0 {
		t.Fatalf("disallowed MCP prompt leaked: %+v", parts)
	}

	parts, err = contributor.ContributePrompt(context.Background(), PromptBuildRequest{
		AllowedMCPServers:  []string{"github"},
		RestrictMCPServers: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 || !strings.Contains(parts[0].Content, "github") {
		t.Fatalf("allowed MCP prompt missing: %+v", parts)
	}
}

func TestPresetCommandPersistentAndTemporarySelection(t *testing.T) {
	cfg := &config.Config{AgentPresets: map[string]config.AgentPresetConfig{"coding": {}}}
	al := &AgentLoop{cfg: cfg}
	agent := &AgentInstance{
		Sessions: session.NewSessionManager(""),
		Tools:    tools.NewToolRegistry(),
	}
	opts := processOptions{Dispatch: DispatchRequest{SessionKey: "session-1", UserMessage: "/preset use coding"}}

	matched, handled, reply := al.applyPresetCommand("/preset use coding", agent, &opts)
	if !matched || !handled || !strings.Contains(reply, "coding") {
		t.Fatalf("use result = (%v, %v, %q)", matched, handled, reply)
	}
	store := agent.Sessions.(session.AgentPresetSessionStore)
	if got := store.GetAgentPreset("session-1"); got != "coding" {
		t.Fatalf("stored preset = %q, want coding", got)
	}

	opts = processOptions{Dispatch: DispatchRequest{SessionKey: "session-1"}}
	matched, handled, reply = al.applyPresetCommand(
		"/preset run coding explain this",
		agent,
		&opts,
	)
	if !matched || handled || reply != "" {
		t.Fatalf("run result = (%v, %v, %q)", matched, handled, reply)
	}
	if opts.Dispatch.UserMessage != "explain this" || opts.AgentPreset.Name != "coding" {
		t.Fatalf("temporary opts = %+v", opts)
	}
	if got := store.GetAgentPreset("session-1"); got != "coding" {
		t.Fatalf("temporary run changed stored preset to %q", got)
	}
}

func TestChannelDefaultPresetAndExplicitSessionDefault(t *testing.T) {
	cfg := &config.Config{
		AgentPresets: map[string]config.AgentPresetConfig{"coding": {}},
		Channels: config.ChannelsConfig{
			"telegram": {Type: config.ChannelTelegram, DefaultPreset: "coding"},
		},
	}
	al := &AgentLoop{cfg: cfg}
	manager := session.NewSessionManager("")
	agent := &AgentInstance{Sessions: manager, Tools: tools.NewToolRegistry()}
	base := processOptions{Dispatch: DispatchRequest{
		SessionKey:     "session-1",
		InboundContext: &bus.InboundContext{Channel: "telegram"},
	}}

	resolved, err := al.resolveAgentPresetOptions(agent, base)
	if err != nil || resolved.AgentPreset.Name != "coding" {
		t.Fatalf("channel default resolution = (%+v, %v), want coding", resolved.AgentPreset, err)
	}

	if err := manager.SetAgentPresetOverride("session-1", "", true); err != nil {
		t.Fatal(err)
	}
	resolved, err = al.resolveAgentPresetOptions(agent, base)
	if err != nil || resolved.AgentPreset.Enabled() {
		t.Fatalf("explicit default resolution = (%+v, %v), want agent default", resolved.AgentPreset, err)
	}

	if err := manager.SetAgentPresetOverride("session-1", "", false); err != nil {
		t.Fatal(err)
	}
	resolved, err = al.resolveAgentPresetOptions(agent, base)
	if err != nil || resolved.AgentPreset.Name != "coding" {
		t.Fatalf("reset resolution = (%+v, %v), want coding", resolved.AgentPreset, err)
	}
}

func TestPresetRunPreservesMessageWhitespace(t *testing.T) {
	cfg := &config.Config{AgentPresets: map[string]config.AgentPresetConfig{"coding": {}}}
	al := &AgentLoop{cfg: cfg}
	agent := &AgentInstance{
		Sessions: session.NewSessionManager(""),
		Tools:    tools.NewToolRegistry(),
	}
	opts := processOptions{Dispatch: DispatchRequest{SessionKey: "session-1"}}

	message := "explain:\n    fmt.Println(\"hello\")"
	matched, handled, reply := al.applyPresetCommand(
		"/preset run coding "+message,
		agent,
		&opts,
	)
	if !matched || handled || reply != "" {
		t.Fatalf("run result = (%v, %v, %q)", matched, handled, reply)
	}
	if opts.Dispatch.UserMessage != message || opts.UserMessage != message {
		t.Fatalf("message = %q, want %q", opts.Dispatch.UserMessage, message)
	}
}

func TestPresetShowFormatsEachCapability(t *testing.T) {
	got := formatAgentPreset(config.EffectiveAgentPreset{
		Name:            "research",
		Tools:           []string{"read_file"},
		ToolsSpecified:  true,
		Skills:          []string{"research"},
		SkillsSpecified: true,
		MCP:             []string{"github"},
		MCPSpecified:    true,
	})
	want := "Agent Preset: research\nModel: inherit\nTools: read_file\nSkills: research\nMCP: github"
	if got != want {
		t.Fatalf("formatAgentPreset() = %q, want %q", got, want)
	}
}

func TestPresetSwitchClearsPendingSkill(t *testing.T) {
	cfg := &config.Config{AgentPresets: map[string]config.AgentPresetConfig{"coding": {}}}
	al := &AgentLoop{cfg: cfg}
	agent := &AgentInstance{
		Sessions: session.NewSessionManager(""),
		Tools:    tools.NewToolRegistry(),
	}
	al.setPendingSkills("session-1", []string{"research"})
	opts := processOptions{Dispatch: DispatchRequest{SessionKey: "session-1"}}

	matched, handled, _ := al.applyPresetCommand("/preset use coding", agent, &opts)
	if !matched || !handled {
		t.Fatalf("use result = (%v, %v)", matched, handled)
	}
	if pending := al.takePendingSkills("session-1"); len(pending) != 0 {
		t.Fatalf("pending skills after preset switch = %v", pending)
	}
}

func TestFilterToolsForTurnStillHonorsTurnProfile(t *testing.T) {
	registry := tools.NewToolRegistry()
	registry.Register(&presetTestTool{name: "read_file"})
	agent := &AgentInstance{Tools: registry}
	preset := config.EffectiveAgentPreset{Name: "all", ToolsSpecified: false}
	turnPolicy := config.EffectiveTurnProfile{
		Enabled:     true,
		ToolsMode:   config.TurnProfileModeOff,
		SkillsMode:  config.TurnProfileModeDefault,
		HistoryMode: config.TurnProfileModeDefault,
	}
	defs := filterToolsForTurn(agent, []providers.ToolDefinition{
		{Type: "function", Function: providers.ToolFunctionDefinition{Name: "read_file"}},
	}, turnPolicy, preset)
	if len(defs) != 0 {
		t.Fatalf("turn profile restriction was bypassed: %+v", defs)
	}
}
