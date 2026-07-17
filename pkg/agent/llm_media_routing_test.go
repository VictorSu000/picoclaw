package agent

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

type mediaRoutingTestProvider struct{}

func (mediaRoutingTestProvider) Chat(
	context.Context,
	[]providers.Message,
	[]providers.ToolDefinition,
	string,
	map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{}, nil
}

func (mediaRoutingTestProvider) GetDefaultModel() string { return "test-model" }

func mediaRoutingTestState(t *testing.T, primaryTags []string) (*Pipeline, *turnState, *turnExecution) {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
			Provider:            "openai",
			ModelName:           "primary-model",
			VisionFallbackModel: "vision-fallback",
		}},
		ModelList: []*config.ModelConfig{
			{ModelName: "primary-model", Provider: "openai", Model: "primary-model", Tags: primaryTags},
			{ModelName: "vision-fallback", Provider: "openai", Model: "vision-fallback", Tags: []string{config.ModelTagVision}},
		},
	}
	primary := resolveModelCandidates(cfg, "openai", "primary-model", nil)
	visionFallback := resolveModelCandidates(cfg, "openai", "vision-fallback", nil)
	agent := &AgentInstance{
		Model:                    "primary-model",
		Candidates:               primary,
		VisionFallbackCandidates: visionFallback,
	}
	exec := &turnExecution{
		activeCandidates:  primary,
		activeModel:       resolvedCandidateModel(primary, "primary-model"),
		activeModelConfig: resolveActiveModelConfig(cfg, "", primary, "primary-model", "openai"),
		activeProvider:    mediaRoutingTestProvider{},
		llmModelName:      "primary-model",
		callMessages: []providers.Message{{
			Role:  "user",
			Media: []string{"data:image/png;base64,abc"},
		}},
	}
	return &Pipeline{Cfg: cfg}, &turnState{agent: agent}, exec
}

func TestRouteMediaTurnKeepsVisionCapablePrimary(t *testing.T) {
	pipeline, state, exec := mediaRoutingTestState(t, []string{config.ModelTagVision})
	originalKey := exec.activeCandidates[0].StableKey()

	if err := pipeline.routeMediaTurn(state, exec); err != nil {
		t.Fatalf("routeMediaTurn() error = %v", err)
	}
	if got := exec.activeCandidates[0].StableKey(); got != originalKey {
		t.Fatalf("active candidate = %q, want primary %q", got, originalKey)
	}
	if exec.llmModelName != "primary-model" {
		t.Fatalf("llmModelName = %q, want primary-model", exec.llmModelName)
	}
}

func TestRouteMediaTurnUsesVisionFallbackForTextPrimary(t *testing.T) {
	pipeline, state, exec := mediaRoutingTestState(t, nil)

	if err := pipeline.routeMediaTurn(state, exec); err != nil {
		t.Fatalf("routeMediaTurn() error = %v", err)
	}
	if exec.llmModelName != "vision-fallback" {
		t.Fatalf("llmModelName = %q, want vision-fallback", exec.llmModelName)
	}
	if exec.activeModelConfig == nil || !exec.activeModelConfig.HasTag(config.ModelTagVision) {
		t.Fatal("active model config is not the vision fallback")
	}
}

func TestRouteMediaTurnImageModelOverridesVisionCapablePrimary(t *testing.T) {
	pipeline, state, exec := mediaRoutingTestState(t, []string{config.ModelTagVision})
	state.agent.ImageCandidates = state.agent.VisionFallbackCandidates
	state.agent.VisionFallbackCandidates = nil
	pipeline.Cfg.Agents.Defaults.ImageModel = "vision-fallback"
	pipeline.Cfg.Agents.Defaults.VisionFallbackModel = ""

	if err := pipeline.routeMediaTurn(state, exec); err != nil {
		t.Fatalf("routeMediaTurn() error = %v", err)
	}
	if exec.llmModelName != "vision-fallback" {
		t.Fatalf("llmModelName = %q, want dedicated image_model", exec.llmModelName)
	}
}
