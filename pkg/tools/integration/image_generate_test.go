package integrationtools

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/media"
)

func TestImageGenerateToolStoresMediaForDirectDelivery(t *testing.T) {
	pngData, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADUlEQVR4nGNgYAAAAAMAASsJTYQAAAAASUVORK5CYII=",
	)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(pngData)}},
		})
	}))
	defer server.Close()

	modelCfg := &config.ModelConfig{
		ModelName: "image-model",
		Provider:  "openai",
		Model:     "gpt-image-test",
		Tags:      []string{config.ModelTagImageGeneration},
		APIBase:   server.URL,
		APIKeys:   config.SimpleSecureStrings("test-key"),
		Enabled:   true,
	}
	cfg := config.DefaultConfig()
	cfg.ModelList = []*config.ModelConfig{modelCfg}
	store := media.NewFileMediaStore()
	tool := NewImageGenerateTool(cfg, "image-model", nil, 4, 1024*1024, store)
	ctx := WithToolContext(t.Context(), "test", "chat-1")

	result := tool.Execute(ctx, map[string]any{
		"prompt":   "draw a pico board",
		"filename": "pico.png",
	})
	if result.IsError {
		t.Fatalf("Execute() error = %s", result.ForLLM)
	}
	if !result.ResponseHandled {
		t.Fatal("ResponseHandled = false, want true")
	}
	if len(result.Media) != 1 {
		t.Fatalf("media refs = %v, want one", result.Media)
	}
	path, meta, err := store.ResolveWithMeta(result.Media[0])
	if err != nil {
		t.Fatalf("ResolveWithMeta() error = %v", err)
	}
	if path == "" || meta.Filename != "pico.png" || meta.ContentType != "image/png" {
		t.Fatalf("stored media = path %q meta %#v", path, meta)
	}
}

func TestImageGenerateToolEditsCurrentTurnInputAndMask(t *testing.T) {
	pngData, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADUlEQVR4nGNgYAAAAAMAASsJTYQAAAAASUVORK5CYII=",
	)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/edits" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(4 * 1024 * 1024); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(r.MultipartForm.File["image"]) != 1 || len(r.MultipartForm.File["mask"]) != 1 {
			http.Error(w, "missing image or mask", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(pngData)}},
		})
	}))
	defer server.Close()

	inputPath := filepath.Join(t.TempDir(), "reference.png")
	maskPath := filepath.Join(t.TempDir(), "mask.png")
	if err := os.WriteFile(inputPath, pngData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(maskPath, pngData, 0o600); err != nil {
		t.Fatal(err)
	}
	store := media.NewFileMediaStore()
	inputRef, err := store.Store(inputPath, media.MediaMeta{
		Filename: "reference.png", ContentType: "image/png",
	}, "test-input")
	if err != nil {
		t.Fatal(err)
	}
	maskRef, err := store.Store(maskPath, media.MediaMeta{
		Filename: "mask.png", ContentType: "image/png",
	}, "test-input")
	if err != nil {
		t.Fatal(err)
	}

	modelCfg := &config.ModelConfig{
		ModelName: "image-model",
		Provider:  "openai",
		Model:     "gpt-image-edit-test",
		Tags:      []string{config.ModelTagImageGeneration},
		APIBase:   server.URL,
		APIKeys:   config.SimpleSecureStrings("test-key"),
		Enabled:   true,
	}
	cfg := config.DefaultConfig()
	cfg.ModelList = []*config.ModelConfig{modelCfg}
	tool := NewImageGenerateTool(cfg, "image-model", nil, 4, 1024*1024, store)
	ctx := WithToolContext(t.Context(), "test", "chat-1")
	ctx = WithToolMediaRefs(ctx, []string{inputRef, maskRef})
	result := tool.Execute(ctx, map[string]any{
		"prompt":       "remove the background",
		"input_images": []any{inputPath},
		"mask":         maskPath,
	})
	if result.IsError {
		t.Fatalf("Execute() error = %s", result.ForLLM)
	}
	if !result.ResponseHandled || len(result.Media) != 1 {
		t.Fatalf("result = %#v, want one handled media ref", result)
	}
}

func TestImageGenerateToolRejectsUnlistedInputPath(t *testing.T) {
	pngData, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADUlEQVR4nGNgYAAAAAMAASsJTYQAAAAASUVORK5CYII=",
	)
	if err != nil {
		t.Fatal(err)
	}
	inputPath := filepath.Join(t.TempDir(), "unlisted.png")
	if err := os.WriteFile(inputPath, pngData, 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewImageGenerateTool(
		config.DefaultConfig(),
		"image-model",
		nil,
		4,
		1024*1024,
		media.NewFileMediaStore(),
	)
	ctx := WithToolContext(t.Context(), "test", "chat-1")
	result := tool.Execute(ctx, map[string]any{
		"prompt":       "edit this image",
		"input_images": []any{inputPath},
	})
	if !result.IsError || !strings.Contains(result.ForLLM, "not available in the current turn") {
		t.Fatalf("result = %#v, want current-turn media error", result)
	}
}
