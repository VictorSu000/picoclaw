package integrationtools

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
