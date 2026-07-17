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

func writeImageGenerationTestConfig(t *testing.T) string {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := config.DefaultConfig()
	cfg.ModelList = []*config.ModelConfig{
		{
			ModelName: "text-model",
			Provider:  "openai",
			Model:     "text-model",
			APIKeys:   config.SimpleSecureStrings("sk-text-model"),
		},
		{
			ModelName: "image-generation-model",
			Provider:  "openai",
			Model:     "gpt-image-test",
			Tags:      []string{config.ModelTagImageGeneration},
			APIKeys:   config.SimpleSecureStrings("sk-image-model"),
		},
	}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	return configPath
}

func TestHandleSetImageGenerationModel(t *testing.T) {
	configPath := writeImageGenerationTestConfig(t)
	handler := NewHandler(configPath)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	invalidRecorder := httptest.NewRecorder()
	invalidRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/models/image-generation",
		bytes.NewBufferString(`{"model_name":"text-model"}`),
	)
	mux.ServeHTTP(invalidRecorder, invalidRequest)
	if invalidRecorder.Code != http.StatusBadRequest {
		t.Fatalf("untagged status = %d, want %d", invalidRecorder.Code, http.StatusBadRequest)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/models/image-generation",
		bytes.NewBufferString(`{"model_name":"image-generation-model"}`),
	)
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Agents.Defaults.ImageGenerationModel != "image-generation-model" {
		t.Fatalf("image_generation_model = %q, want image-generation-model", cfg.Agents.Defaults.ImageGenerationModel)
	}
	if !cfg.Tools.ImageGenerate.Enabled {
		t.Fatal("image_generate tool was not enabled")
	}

	clearRecorder := httptest.NewRecorder()
	clearRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/models/image-generation",
		bytes.NewBufferString(`{"model_name":""}`),
	)
	mux.ServeHTTP(clearRecorder, clearRequest)
	if clearRecorder.Code != http.StatusOK {
		t.Fatalf("clear status = %d, want %d, body=%s", clearRecorder.Code, http.StatusOK, clearRecorder.Body.String())
	}
	cfg, err = config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() after clear error = %v", err)
	}
	if cfg.Agents.Defaults.ImageGenerationModel != "" || cfg.Tools.ImageGenerate.Enabled {
		t.Fatalf(
			"clear left image_generation_model=%q enabled=%v",
			cfg.Agents.Defaults.ImageGenerationModel,
			cfg.Tools.ImageGenerate.Enabled,
		)
	}
}

func TestHandleListModelsReturnsImageGenerationModel(t *testing.T) {
	configPath := writeImageGenerationTestConfig(t)
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.Agents.Defaults.ImageGenerationModel = "image-generation-model"
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	handler := NewHandler(configPath)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response struct {
		Models               []modelResponse `json:"models"`
		ImageGenerationModel string          `json:"image_generation_model"`
	}
	if err = json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if response.ImageGenerationModel != "image-generation-model" {
		t.Fatalf("image_generation_model = %q, want image-generation-model", response.ImageGenerationModel)
	}
	for _, model := range response.Models {
		if model.ModelName == "image-generation-model" {
			if !model.IsImageGeneration {
				t.Fatal("image model is_image_generation = false, want true")
			}
			return
		}
	}
	t.Fatal("image generation model missing from response")
}
