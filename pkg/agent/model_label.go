package agent

import (
	"strings"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// upstreamModelSeparator joins the configured model alias with the model a
// gateway actually routed the request to. It is deliberately a non-ASCII arrow:
// model identifiers never contain it, so the composite stays unambiguous.
const upstreamModelSeparator = " → "

// composeModelLabel builds the model label persisted with a message and shown
// in the WebUI. It falls back to the configured alias alone whenever the
// gateway did not report a different model, so non-proxied setups are
// completely unaffected.
//
// upstreamProvider is the provider slug the gateway reported (e.g. "anthropic")
// and is rendered as a `provider/model` prefix when it adds information.
func composeModelLabel(configuredName, requestedModel, upstreamModel, upstreamProvider string) string {
	alias := strings.TrimSpace(configuredName)
	routed := routedModelID(upstreamModel, upstreamProvider)
	if routed == "" || sameModelID(routed, requestedModel) {
		return alias
	}
	if alias == "" || strings.Contains(alias, upstreamModelSeparator) {
		return routed
	}
	return alias + upstreamModelSeparator + routed
}

// routedModelID renders the routed model as `provider/model`, dropping the
// provider when the model id already carries it.
func routedModelID(model, provider string) string {
	trimmedModel := strings.TrimSpace(model)
	trimmedProvider := strings.ToLower(strings.TrimSpace(provider))
	if trimmedModel == "" {
		return trimmedProvider
	}
	if trimmedProvider == "" {
		return trimmedModel
	}
	if prefix, _, ok := strings.Cut(trimmedModel, "/"); ok && strings.EqualFold(prefix, trimmedProvider) {
		return trimmedModel
	}
	return trimmedProvider + "/" + trimmedModel
}

// sameModelID compares two model identifiers, ignoring case and the provider
// prefix (e.g. "openai/gpt-4o" and "gpt-4o" are the same model).
func sameModelID(a, b string) bool {
	left := normalizeModelID(a)
	right := normalizeModelID(b)
	return left != "" && left == right
}

func normalizeModelID(model string) string {
	trimmed := strings.ToLower(strings.TrimSpace(model))
	if _, after, ok := strings.Cut(trimmed, "/"); ok {
		return strings.TrimSpace(after)
	}
	return trimmed
}

// upstreamRouteOf reads the gateway-reported routing metadata off a response,
// tolerating nil results from error paths.
func upstreamRouteOf(response *providers.LLMResponse) (model, provider string) {
	if response == nil {
		return "", ""
	}
	return response.UpstreamModel, response.UpstreamProvider
}
