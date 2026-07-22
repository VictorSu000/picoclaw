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

func TestImageGenerateToolInstructionsDistinguishAttachmentsFromLocalPaths(t *testing.T) {
	tool := NewImageGenerateTool(config.DefaultConfig(), "image-model", nil, 4, 1024, media.NewFileMediaStore())

	description := tool.Description()
	for _, want := range []string{"current_image_N", "do not call load_image or read_file", "call load_image with that path first"} {
		if !strings.Contains(description, want) {
			t.Fatalf("Description() = %q, want it to contain %q", description, want)
		}
	}

	properties := tool.Parameters()["properties"].(map[string]any)
	inputImages := properties["input_images"].(map[string]any)
	parameterDescription := inputImages["description"].(string)
	for _, want := range []string{"current_image_1", "already attached image", "call load_image on that path first"} {
		if !strings.Contains(parameterDescription, want) {
			t.Fatalf("input_images description = %q, want it to contain %q", parameterDescription, want)
		}
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
	pathInput, resolvedRef, err := tool.resolveInputImageWithRef(ctx, inputPath)
	if err != nil {
		t.Fatalf("resolveInputImageWithRef(path) error = %v", err)
	}
	if resolvedRef != inputRef || len(pathInput.Data) != len(pngData) {
		t.Fatalf("resolved path = ref %q, bytes %d; want ref %q, bytes %d", resolvedRef, len(pathInput.Data), inputRef, len(pngData))
	}
	result := tool.Execute(ctx, map[string]any{
		"prompt":       "remove the background",
		"input_images": []any{"current_image_1"},
		"mask":         "current_image_2",
	})
	if result.IsError {
		t.Fatalf("Execute() error = %s", result.ForLLM)
	}
	if !result.ResponseHandled || len(result.Media) != 1 {
		t.Fatalf("result = %#v, want one handled media ref", result)
	}
}

func TestImageGenerateToolEditsInlineCurrentTurnImageByAlias(t *testing.T) {
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
		files := r.MultipartForm.File["image"]
		if len(files) != 1 || files[0].Filename != "current_image_1.png" || files[0].Size != int64(len(pngData)) {
			http.Error(w, "unexpected inline input image", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(pngData)}},
		})
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "image-model",
		Provider:  "openai",
		Model:     "gpt-image-edit-test",
		Tags:      []string{config.ModelTagImageGeneration},
		APIBase:   server.URL,
		APIKeys:   config.SimpleSecureStrings("test-key"),
		Enabled:   true,
	}}
	store := media.NewFileMediaStore()
	tool := NewImageGenerateTool(cfg, "image-model", nil, 4, 1024*1024, store)
	ctx := WithToolContext(t.Context(), "test", "chat-1")
	ctx = WithToolMediaRefs(ctx, []string{
		"data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData),
	})

	result := tool.Execute(ctx, map[string]any{
		"prompt":       "remove the background",
		"input_images": []any{"current_image_1"},
	})
	if result.IsError {
		t.Fatalf("Execute() error = %s", result.ForLLM)
	}
	if !result.ResponseHandled || len(result.Media) != 1 {
		t.Fatalf("result = %#v, want one handled media ref", result)
	}
}

func TestImageGenerateToolRejectsInvalidInlineCurrentTurnImage(t *testing.T) {
	tool := NewImageGenerateTool(
		config.DefaultConfig(),
		"image-model",
		nil,
		4,
		1024*1024,
		media.NewFileMediaStore(),
	)
	ctx := WithToolContext(t.Context(), "test", "chat-1")
	ctx = WithToolMediaRefs(ctx, []string{"data:image/png;base64,bm90LWEtcG5n"})

	result := tool.Execute(ctx, map[string]any{
		"prompt":       "edit this image",
		"input_images": []any{"current_image_1"},
	})
	if !result.IsError || !strings.Contains(result.ForLLM, "unsupported or unrecognized image type") {
		t.Fatalf("result = %#v, want invalid inline image error", result)
	}
}

func TestReadInlineImageGenerationInputRejectsOversizedImage(t *testing.T) {
	pngData, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADUlEQVR4nGNgYAAAAAMAASsJTYQAAAAASUVORK5CYII=",
	)
	if err != nil {
		t.Fatal(err)
	}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData)

	_, err = readInlineImageGenerationInput(dataURL, 1, len(pngData)-1)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("readInlineImageGenerationInput() error = %v, want size error", err)
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
