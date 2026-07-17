package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func writeVisionFallbackTestConfig(t *testing.T) string {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.ImageModel = "image-model"
	cfg.ModelList = []*config.ModelConfig{
		{
			ModelName: "text-model",
			Provider:  "openai",
			Model:     "text-model",
			APIKeys:   config.SimpleSecureStrings("sk-text-model"),
		},
		{
			ModelName: "image-model",
			Provider:  "openai",
			Model:     "image-model",
			APIKeys:   config.SimpleSecureStrings("sk-image-model"),
		},
		{
			ModelName: "vision-model",
			Provider:  "openai",
			Model:     "vision-model",
			Tags:      []string{config.ModelTagVision},
			APIKeys:   config.SimpleSecureStrings("sk-vision-model"),
		},
	}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	return configPath
}

func TestHandleSetVisionFallbackModel(t *testing.T) {
	configPath := writeVisionFallbackTestConfig(t)
	handler := NewHandler(configPath)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	invalidRecorder := httptest.NewRecorder()
	invalidRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/models/vision-fallback",
		bytes.NewBufferString(`{"model_name":"text-model"}`),
	)
	mux.ServeHTTP(invalidRecorder, invalidRequest)
	if invalidRecorder.Code != http.StatusBadRequest {
		t.Fatalf("untagged status = %d, want %d", invalidRecorder.Code, http.StatusBadRequest)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/models/vision-fallback",
		bytes.NewBufferString(`{"model_name":"vision-model"}`),
	)
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Agents.Defaults.VisionFallbackModel != "vision-model" {
		t.Fatalf("vision_fallback_model = %q, want vision-model", cfg.Agents.Defaults.VisionFallbackModel)
	}
	if cfg.Agents.Defaults.ImageModel != "image-model" {
		t.Fatalf("image_model = %q, want original image-model", cfg.Agents.Defaults.ImageModel)
	}
}

func TestHandleListModelsReturnsTagsAndVisionFallback(t *testing.T) {
	configPath := writeVisionFallbackTestConfig(t)
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.Agents.Defaults.VisionFallbackModel = "vision-model"
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	handler := NewHandler(configPath)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response struct {
		Models              []modelResponse `json:"models"`
		VisionFallbackModel string          `json:"vision_fallback_model"`
	}
	if err = json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if response.VisionFallbackModel != "vision-model" {
		t.Fatalf("vision_fallback_model = %q, want vision-model", response.VisionFallbackModel)
	}
	for _, model := range response.Models {
		if model.ModelName != "vision-model" {
			continue
		}
		if len(model.Tags) != 1 || model.Tags[0] != config.ModelTagVision {
			t.Fatalf("vision model tags = %#v, want [vision]", model.Tags)
		}
		if !model.IsVisionFallback {
			t.Fatal("vision model is_vision_fallback = false, want true")
		}
		return
	}
	t.Fatal("vision model missing from response")
}
