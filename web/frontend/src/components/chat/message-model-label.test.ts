/// <reference types="node" />
import assert from "node:assert/strict"
import test from "node:test"

import { parseMessageModelLabel } from "./message-model-label.ts"

test("parseMessageModelLabel returns only the configured alias when no gateway routed the request", () => {
  assert.deepEqual(parseMessageModelLabel("my-claude"), {
    configured: "my-claude",
    routed: "",
  })
})

test("parseMessageModelLabel splits the gateway reported model", () => {
  assert.deepEqual(parseMessageModelLabel("my-claude → claude-sonnet-4-5"), {
    configured: "my-claude",
    routed: "claude-sonnet-4-5",
  })
})

test("parseMessageModelLabel keeps the gateway reported provider", () => {
  assert.deepEqual(
    parseMessageModelLabel("my-claude → anthropic/claude-sonnet-4-5"),
    { configured: "my-claude", routed: "anthropic/claude-sonnet-4-5" },
  )
})

test("parseMessageModelLabel keeps aliases that contain slashes intact", () => {
  assert.deepEqual(
    parseMessageModelLabel("openai/gpt-4o → gpt-4o-2024-08-06"),
    {
      configured: "openai/gpt-4o",
      routed: "gpt-4o-2024-08-06",
    },
  )
})

test("parseMessageModelLabel handles missing input", () => {
  assert.deepEqual(parseMessageModelLabel(undefined), {
    configured: "",
    routed: "",
  })
  assert.deepEqual(parseMessageModelLabel("   "), {
    configured: "",
    routed: "",
  })
})
