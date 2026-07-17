package api

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestConfigSignatureTracksVisionRoutingChanges(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
			Provider:  "openai",
			ModelName: "primary-model",
		}},
		ModelList: []*config.ModelConfig{
			{ModelName: "primary-model", Provider: "openai", Model: "primary-model"},
			{ModelName: "vision-model", Provider: "openai", Model: "vision-model", Tags: []string{config.ModelTagVision}},
		},
	}

	original := computeConfigSignature(cfg)
	cfg.ModelList[0].Tags = []string{config.ModelTagVision}
	withPrimaryVisionTag := computeConfigSignature(cfg)
	if original == withPrimaryVisionTag {
		t.Fatal("config signature did not change when the primary model gained the vision tag")
	}

	cfg.Agents.Defaults.ImageModel = "vision-model"
	withImageModel := computeConfigSignature(cfg)
	if withPrimaryVisionTag == withImageModel {
		t.Fatal("config signature did not change when image_model was configured")
	}

	cfg.Agents.Defaults.VisionFallbackModel = "vision-model"
	withVisionFallback := computeConfigSignature(cfg)
	if withImageModel == withVisionFallback {
		t.Fatal("config signature did not change when the vision fallback was configured")
	}
}
