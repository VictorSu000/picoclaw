package openai_compat

import (
	"net/http"
	"strings"
)

const (
	// defaultUpstreamModelHeader and defaultUpstreamProviderHeader are the
	// response headers Cloudflare AI Gateway uses to report which backend
	// actually served the request. Gateways that never swap models do not send
	// them, so their presence is also the signal that the request was proxied.
	defaultUpstreamModelHeader    = "cf-aig-model"
	defaultUpstreamProviderHeader = "cf-aig-provider"

	// aiGatewayHeaderPrefix marks a response as coming from Cloudflare AI
	// Gateway. Every AI Gateway response carries at least cf-aig-log-id, which
	// makes this a reliable "this was proxied" detector.
	aiGatewayHeaderPrefix = "cf-aig-"
)

// upstreamRouteCapture carries gateway routing metadata out of a response.
// The headers win; the response body's `model` field is only consulted as a
// fallback for proxies that only rewrite the body.
type upstreamRouteCapture struct {
	model    string
	provider string
	proxied  bool
}

// newUpstreamRouteCapture inspects response headers and returns nil when the
// response cannot have been rewritten, which keeps LLMResponse.UpstreamModel
// and LLMResponse.UpstreamProvider empty for direct provider calls.
func newUpstreamRouteCapture(header http.Header, modelHeader, providerHeader string) *upstreamRouteCapture {
	configuredModel := strings.TrimSpace(modelHeader)
	if !isProxiedResponse(header, configuredModel, strings.TrimSpace(providerHeader)) {
		return nil
	}

	if configuredModel == "" {
		configuredModel = defaultUpstreamModelHeader
	}
	if strings.TrimSpace(providerHeader) == "" {
		providerHeader = defaultUpstreamProviderHeader
	}

	capture := &upstreamRouteCapture{proxied: true}
	if header != nil {
		capture.model = strings.TrimSpace(header.Get(configuredModel))
		capture.provider = strings.TrimSpace(header.Get(providerHeader))
	}
	return capture
}

// isProxiedResponse reports whether the response came from a gateway that may
// rewrite the model. Explicitly configured headers always count, otherwise we
// look for Cloudflare AI Gateway markers.
func isProxiedResponse(header http.Header, configuredHeaders ...string) bool {
	for _, configured := range configuredHeaders {
		if strings.TrimSpace(configured) != "" {
			return true
		}
	}
	if header == nil {
		return false
	}
	for name := range header {
		if strings.HasPrefix(strings.ToLower(name), aiGatewayHeaderPrefix) {
			return true
		}
	}
	return false
}

// observeBodyModel records the response body's `model` field. It is ignored
// unless the response is known to be proxied, so plain provider responses that
// echo the requested model never look like a rewrite.
func (c *upstreamRouteCapture) observeBodyModel(model string) {
	if c == nil || !c.proxied || c.model != "" {
		return
	}
	c.model = strings.TrimSpace(model)
}

// resolveModel returns the upstream model to report, falling back to the model
// the body echoed back. It returns "" when nothing was reported.
func (c *upstreamRouteCapture) resolveModel(bodyModel string) string {
	if c == nil {
		return ""
	}
	if c.model != "" {
		return c.model
	}
	return strings.TrimSpace(bodyModel)
}

// resolveProvider returns the upstream provider slug, or "" when the gateway
// did not report one.
func (c *upstreamRouteCapture) resolveProvider() string {
	if c == nil {
		return ""
	}
	return c.provider
}

// upstreamChunk stamps gateway routing metadata onto a stream chunk. It is a
// nil-safe no-op for direct provider calls, and safe on a nil capture.
func upstreamChunk(route *upstreamRouteCapture, chunk StreamChunk) StreamChunk {
	chunk.UpstreamModel = route.resolveModel("")
	chunk.UpstreamProvider = route.resolveProvider()
	return chunk
}
