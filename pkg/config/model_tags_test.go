package config

import (
	"encoding/json"
	"testing"
)

func TestModelConfigHasTag(t *testing.T) {
	model := &ModelConfig{Tags: []string{" tools ", "Vision"}}
	if !model.HasTag(ModelTagVision) {
		t.Fatal("HasTag(vision) = false, want true for a case-insensitive match")
	}
	if model.HasTag("audio") {
		t.Fatal("HasTag(audio) = true, want false")
	}
	if (*ModelConfig)(nil).HasTag(ModelTagVision) {
		t.Fatal("nil ModelConfig reported a tag")
	}
}

func TestExpandMultiKeyModelsPreservesTags(t *testing.T) {
	models := []*ModelConfig{{
		ModelName: "vision-model",
		Model:     "openai/vision-model",
		Tags:      []string{ModelTagVision},
		APIKeys:   SimpleSecureStrings("key-1", "key-2"),
	}}

	expanded := expandMultiKeyModels(models)
	if len(expanded) != 2 {
		t.Fatalf("len(expanded) = %d, want 2", len(expanded))
	}
	for i, model := range expanded {
		if !model.HasTag(ModelTagVision) {
			t.Fatalf("expanded[%d] lost the vision tag", i)
		}
	}
}

func TestAgentDefaultsVisionFallbackModelJSON(t *testing.T) {
	var defaults AgentDefaults
	if err := json.Unmarshal(
		[]byte(`{"image_model":"dedicated-image","vision_fallback_model":"conditional-vision"}`),
		&defaults,
	); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if defaults.ImageModel != "dedicated-image" {
		t.Fatalf("ImageModel = %q, want dedicated-image", defaults.ImageModel)
	}
	if defaults.VisionFallbackModel != "conditional-vision" {
		t.Fatalf("VisionFallbackModel = %q, want conditional-vision", defaults.VisionFallbackModel)
	}
}
