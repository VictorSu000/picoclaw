// Model identifiers never contain an arrow, so this separator is unambiguous.
// It must stay in sync with upstreamModelSeparator in pkg/agent/model_label.go.
const UPSTREAM_MODEL_SEPARATOR = " → "

export interface MessageModelLabel {
  /** The configured model alias. */
  configured: string
  /**
   * The route a gateway actually served, as `provider/model` when the gateway
   * reported a provider. Empty for direct provider calls.
   */
  routed: string
}

/**
 * Splits a persisted `model_name` into the configured alias and the route a
 * gateway actually served. Messages recorded without a gateway carry only the
 * alias, so `routed` is empty and callers render a single name as before.
 */
export function parseMessageModelLabel(
  modelName: string | undefined | null,
): MessageModelLabel {
  const trimmed = (modelName ?? "").trim()
  if (!trimmed) {
    return { configured: "", routed: "" }
  }

  const index = trimmed.indexOf(UPSTREAM_MODEL_SEPARATOR)
  if (index < 0) {
    return { configured: trimmed, routed: "" }
  }

  return {
    configured: trimmed.slice(0, index).trim(),
    routed: trimmed.slice(index + UPSTREAM_MODEL_SEPARATOR.length).trim(),
  }
}
