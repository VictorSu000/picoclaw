package tools

import (
	"context"
	"strings"
	"testing"
)

type presetMCPTool struct {
	name   string
	server string
}

func (t *presetMCPTool) Name() string        { return t.name }
func (t *presetMCPTool) Description() string { return "search from " + t.server }
func (t *presetMCPTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (t *presetMCPTool) Execute(context.Context, map[string]any) *ToolResult {
	return SilentResult("ok")
}
func (t *presetMCPTool) MCPServerName() string { return t.server }

func TestDeferredSearchRespectsAllowedMCPServers(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterHidden(&presetMCPTool{name: "mcp_github_search", server: "github"})
	registry.RegisterHidden(&presetMCPTool{name: "mcp_gitlab_search", server: "gitlab"})
	search := NewRegexSearchTool(registry, 5, 10)

	ctx := WithAllowedMCPServers(context.Background(), []string{"github"}, true)
	result := search.Execute(ctx, map[string]any{"pattern": "search"})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "mcp_github_search") {
		t.Fatalf("allowed MCP tool missing: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "mcp_gitlab_search") {
		t.Fatalf("disallowed MCP tool leaked: %s", result.ForLLM)
	}
}

func TestEmptyAllowedMCPServersReturnsNoDeferredTools(t *testing.T) {
	registry := NewToolRegistry()
	registry.RegisterHidden(&presetMCPTool{name: "mcp_github_search", server: "github"})
	search := NewBM25SearchTool(registry, 5, 10)

	ctx := WithAllowedMCPServers(context.Background(), nil, true)
	result := search.Execute(ctx, map[string]any{"query": "github search"})
	if result.IsError || !strings.Contains(result.ForLLM, "No tools found") {
		t.Fatalf("unexpected result: %+v", result)
	}
}
