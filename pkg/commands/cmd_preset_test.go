package commands

import (
	"context"
	"strings"
	"testing"
)

func TestPresetDefinitionIsRegisteredAndHandledByAgentLoop(t *testing.T) {
	defs := BuiltinDefinitions()
	def := findDefinitionByName(t, defs, "preset")
	if !strings.Contains(def.EffectiveUsage(), "/preset") {
		t.Fatalf("preset usage = %q", def.EffectiveUsage())
	}

	executor := NewExecutor(NewRegistry(defs), nil)
	result := executor.Execute(context.Background(), Request{Text: "/preset list"})
	if result.Outcome != OutcomePassthrough || result.Command != "preset" {
		t.Fatalf("preset executor result = %+v, want agent-loop passthrough", result)
	}
}
