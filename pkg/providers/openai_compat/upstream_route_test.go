package openai_compat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func headerFrom(values map[string]string) http.Header {
	header := http.Header{}
	for name, value := range values {
		header.Set(name, value)
	}
	return header
}

func TestNewUpstreamRouteCapture(t *testing.T) {
	tests := []struct {
		name          string
		header        map[string]string
		modelHeader   string
		providerHeadr string
		wantModel     string
		wantProvider  string
	}{
		{
			name:   "direct provider has no gateway headers",
			header: map[string]string{"Content-Type": "application/json"},
		},
		{
			name:         "ai gateway reports the routed model and provider",
			header:       map[string]string{"cf-aig-model": "claude-sonnet-4-5", "cf-aig-provider": "anthropic", "cf-aig-log-id": "abc"},
			wantModel:    "claude-sonnet-4-5",
			wantProvider: "anthropic",
		},
		{
			name:   "ai gateway without routing headers stays empty",
			header: map[string]string{"cf-aig-log-id": "abc"},
		},
		{
			name:          "configured headers override auto detection",
			header:        map[string]string{"X-Upstream-Model": "glm-4.6", "X-Upstream-Provider": "Zhipu"},
			modelHeader:   "x-upstream-model",
			providerHeadr: "x-upstream-provider",
			wantModel:     "glm-4.6",
			wantProvider:  "Zhipu",
		},
		{
			name:         "configured model header wins over gateway header",
			header:       map[string]string{"cf-aig-model": "claude-sonnet-4-5", "X-Model": "other"},
			modelHeader:  "x-model",
			wantModel:    "other",
			wantProvider: "",
		},
		{
			name:        "configured header missing yields empty",
			header:      map[string]string{"cf-aig-model": "claude-sonnet-4-5"},
			modelHeader: "x-missing",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			route := newUpstreamRouteCapture(headerFrom(tc.header), tc.modelHeader, tc.providerHeadr)
			if got := route.resolveModel(""); got != tc.wantModel {
				t.Fatalf("resolveModel = %q, want %q", got, tc.wantModel)
			}
			if got := route.resolveProvider(); got != tc.wantProvider {
				t.Fatalf("resolveProvider = %q, want %q", got, tc.wantProvider)
			}
		})
	}
}

func TestUpstreamRouteCaptureBodyFallback(t *testing.T) {
	t.Run("body model is used for proxied responses", func(t *testing.T) {
		route := newUpstreamRouteCapture(headerFrom(map[string]string{"Cf-Aig-Log-Id": "abc"}), "", "")
		route.observeBodyModel("claude-opus-4-1")
		if got := route.resolveModel(""); got != "claude-opus-4-1" {
			t.Fatalf("resolveModel = %q, want %q", got, "claude-opus-4-1")
		}
	})

	t.Run("header wins over body", func(t *testing.T) {
		route := newUpstreamRouteCapture(headerFrom(map[string]string{"Cf-Aig-Model": "from-header"}), "", "")
		route.observeBodyModel("from-body")
		if got := route.resolveModel(""); got != "from-header" {
			t.Fatalf("resolveModel = %q, want %q", got, "from-header")
		}
	})

	t.Run("body model ignored for direct providers", func(t *testing.T) {
		route := newUpstreamRouteCapture(headerFrom(map[string]string{"Content-Type": "application/json"}), "", "")
		route.observeBodyModel("gpt-4o-2024-08-06")
		if got := route.resolveModel(""); got != "" {
			t.Fatalf("resolveModel = %q, want empty", got)
		}
	})

	t.Run("nil capture is safe", func(t *testing.T) {
		var route *upstreamRouteCapture
		route.observeBodyModel("whatever")
		if got := route.resolveModel("body"); got != "" {
			t.Fatalf("resolveModel = %q, want empty", got)
		}
		if got := route.resolveProvider(); got != "" {
			t.Fatalf("resolveProvider = %q, want empty", got)
		}
	})
}

func TestChatStreamUpstreamRouteOnFirstChunk(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("cf-aig-model", "claude-sonnet-4-5")
		w.Header().Set("cf-aig-provider", "anthropic")
		w.Header().Set("cf-aig-log-id", "log-1")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" there\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	provider := NewProvider("key", server.URL, "")

	var chunks []StreamChunk
	response, err := provider.ChatStreamEvents(
		context.Background(),
		[]Message{{Role: "user", Content: "hello"}},
		nil,
		"dynamic/route",
		nil,
		func(chunk StreamChunk) { chunks = append(chunks, chunk) },
	)
	if err != nil {
		t.Fatalf("ChatStreamEvents error: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}
	if chunks[0].UpstreamModel != "claude-sonnet-4-5" || chunks[0].UpstreamProvider != "anthropic" {
		t.Fatalf("first chunk route = %q/%q, want claude-sonnet-4-5/anthropic",
			chunks[0].UpstreamModel, chunks[0].UpstreamProvider)
	}
	if response.UpstreamModel != "claude-sonnet-4-5" || response.UpstreamProvider != "anthropic" {
		t.Fatalf("response route = %q/%q", response.UpstreamModel, response.UpstreamProvider)
	}
}

func TestChatStreamIgnoresBodyModelForDirectProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"model\":\"gpt-4o-2024-08-06\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	provider := NewProvider("key", server.URL, "")
	response, err := provider.ChatStreamEvents(
		context.Background(),
		[]Message{{Role: "user", Content: "hello"}},
		nil,
		"gpt-4o",
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("ChatStreamEvents error: %v", err)
	}
	if response.UpstreamModel != "" || response.UpstreamProvider != "" {
		t.Fatalf("route = %q/%q, want empty for direct provider", response.UpstreamModel, response.UpstreamProvider)
	}
}

func TestChatUpstreamRoute(t *testing.T) {
	tests := []struct {
		name         string
		headers      map[string]string
		bodyModel    string
		wantModel    string
		wantProvider string
	}{
		{
			name:         "gateway headers win",
			headers:      map[string]string{"cf-aig-model": "claude-sonnet-4-5", "cf-aig-provider": "anthropic"},
			bodyModel:    "ignored",
			wantModel:    "claude-sonnet-4-5",
			wantProvider: "anthropic",
		},
		{
			name:      "gateway without model header falls back to body",
			headers:   map[string]string{"cf-aig-log-id": "log-1", "cf-aig-provider": "anthropic"},
			bodyModel: "claude-opus-4-1",
			wantModel: "claude-opus-4-1",

			wantProvider: "anthropic",
		},
		{
			name:      "direct provider reports nothing",
			bodyModel: "gpt-4o-2024-08-06",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(
				`{"model":%q,"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}]}`,
				tc.bodyModel,
			)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for name, value := range tc.headers {
					w.Header().Set(name, value)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, body)
			}))
			defer server.Close()

			provider := NewProvider("key", server.URL, "")
			response, err := provider.Chat(
				context.Background(),
				[]Message{{Role: "user", Content: "hello"}},
				nil,
				"dynamic/route",
				nil,
			)
			if err != nil {
				t.Fatalf("Chat error: %v", err)
			}
			if response.UpstreamModel != tc.wantModel {
				t.Fatalf("UpstreamModel = %q, want %q", response.UpstreamModel, tc.wantModel)
			}
			if response.UpstreamProvider != tc.wantProvider {
				t.Fatalf("UpstreamProvider = %q, want %q", response.UpstreamProvider, tc.wantProvider)
			}
		})
	}
}

func TestSetUpstreamRouteHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Routed-Model", "glm-4.6")
		w.Header().Set("X-Routed-Provider", "zhipu")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	provider := NewProvider("key", server.URL, "")
	provider.SetUpstreamRouteHeaders(" x-routed-model ", " x-routed-provider ")

	response, err := provider.Chat(context.Background(), nil, nil, "my-model", nil)
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if response.UpstreamModel != "glm-4.6" {
		t.Fatalf("UpstreamModel = %q, want %q", response.UpstreamModel, "glm-4.6")
	}
	if response.UpstreamProvider != "zhipu" {
		t.Fatalf("UpstreamProvider = %q, want %q", response.UpstreamProvider, "zhipu")
	}
}

func TestIsProxiedResponseRequiresGatewaySignal(t *testing.T) {
	if isProxiedResponse(headerFrom(map[string]string{"Server": "cloudflare"})) {
		t.Fatal("a generic Cloudflare edge header must not enable upstream route reporting")
	}
	if !isProxiedResponse(headerFrom(map[string]string{"Cf-Aig-Step": "1"})) {
		t.Fatal("cf-aig-step must enable upstream route reporting")
	}
	if isProxiedResponse(nil) {
		t.Fatal("nil header must not enable upstream route reporting")
	}
	if !isProxiedResponse(nil, "", "x-model") {
		t.Fatal("an explicitly configured header must enable upstream route reporting")
	}
}
