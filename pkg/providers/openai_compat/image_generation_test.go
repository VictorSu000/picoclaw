package openai_compat

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers/protocoltypes"
)

var testGeneratedPNG, _ = base64.StdEncoding.DecodeString(
	"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADUlEQVR4nGNgYAAAAAMAASsJTYQAAAAASUVORK5CYII=",
)

func TestProviderGenerateImageDecodesBase64Response(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{
				"b64_json":       base64.StdEncoding.EncodeToString(testGeneratedPNG),
				"revised_prompt": "revised",
			}},
		})
	}))
	defer server.Close()

	provider := NewProvider("test-key", server.URL, "", WithExtraBody(map[string]any{
		"response_format": "b64_json",
		"model":           "must-not-override",
	}))
	response, err := provider.GenerateImage(t.Context(), "gpt-image-test", protocoltypes.ImageGenerationRequest{
		Prompt:       "draw a pico board",
		Count:        1,
		Size:         "1024x1024",
		OutputFormat: "png",
		MaxImageSize: 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("GenerateImage() error = %v", err)
	}
	if got := requestBody["model"]; got != "gpt-image-test" {
		t.Fatalf("model = %#v, want gpt-image-test", got)
	}
	if got := requestBody["response_format"]; got != "b64_json" {
		t.Fatalf("response_format = %#v, want b64_json", got)
	}
	if len(response.Images) != 1 {
		t.Fatalf("image count = %d, want 1", len(response.Images))
	}
	if response.Images[0].ContentType != "image/png" {
		t.Fatalf("content type = %q, want image/png", response.Images[0].ContentType)
	}
	if response.Images[0].RevisedPrompt != "revised" {
		t.Fatalf("revised prompt = %q, want revised", response.Images[0].RevisedPrompt)
	}
}

func TestProviderGenerateImageDownloadsSameHostURLResponse(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/images/generations":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"url": server.URL + "/generated.png"}},
			})
		case "/generated.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(testGeneratedPNG)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := NewProvider("test-key", server.URL, "")
	response, err := provider.GenerateImage(t.Context(), "gpt-image-test", protocoltypes.ImageGenerationRequest{
		Prompt:       "draw a pico board",
		MaxImageSize: 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("GenerateImage() error = %v", err)
	}
	if len(response.Images) != 1 || response.Images[0].ContentType != "image/png" {
		t.Fatalf("images = %#v, want one PNG", response.Images)
	}
}
