import assert from "node:assert/strict"
import test from "node:test"

import {
  type AgentPresetForm,
  buildAgentPresetsMergePatch,
  buildFormFromConfig,
} from "./form-model.ts"

function preset(
  name: string,
  overrides: Partial<AgentPresetForm> = {},
): AgentPresetForm {
  return {
    id: `preset-${name}`,
    name,
    modelMode: "inherit",
    primaryModel: "",
    fallbackModels: [],
    toolsMode: "inherit",
    tools: [],
    skillsMode: "inherit",
    skills: [],
    mcpMode: "inherit",
    mcpServers: [],
    ...overrides,
  }
}

test("buildFormFromConfig preserves inherited and explicitly empty preset fields", () => {
  const form = buildFormFromConfig({
    agent_presets: {
      coding: {
        model: {
          primary: "primary-model",
          fallbacks: ["fallback-model"],
        },
        tools: [],
        skills: ["code-review"],
      },
    },
  })

  assert.equal(form.agentPresets.length, 1)
  assert.deepEqual(form.agentPresets[0], {
    id: "preset-coding",
    name: "coding",
    modelMode: "custom",
    primaryModel: "primary-model",
    fallbackModels: ["fallback-model"],
    toolsMode: "custom",
    tools: [],
    skillsMode: "custom",
    skills: ["code-review"],
    mcpMode: "inherit",
    mcpServers: [],
  })
})

test("buildFormFromConfig supports the compact preset model string", () => {
  const form = buildFormFromConfig({
    agent_presets: {
      compact: { model: "primary-model" },
    },
  })

  assert.equal(form.agentPresets[0]?.modelMode, "custom")
  assert.equal(form.agentPresets[0]?.primaryModel, "primary-model")
  assert.deepEqual(form.agentPresets[0]?.fallbackModels, [])
})

test("buildAgentPresetsMergePatch deletes renamed presets and preserves override semantics", () => {
  const baseline = [preset("old-name")]
  const current = [
    preset("coding", {
      modelMode: "custom",
      primaryModel: " primary-model ",
      fallbackModels: ["fallback-model", "FALLBACK-MODEL"],
      toolsMode: "custom",
      tools: [],
      skillsMode: "custom",
      skills: ["code-review"],
      mcpMode: "custom",
      mcpServers: ["filesystem"],
    }),
  ]

  assert.deepEqual(buildAgentPresetsMergePatch(current, baseline), {
    "old-name": null,
    coding: {
      model: {
        primary: "primary-model",
        fallbacks: ["fallback-model"],
      },
      tools: [],
      skills: ["code-review"],
      mcp: ["filesystem"],
    },
  })
})

test("buildAgentPresetsMergePatch rejects reserved and duplicate names", () => {
  assert.throws(
    () => buildAgentPresetsMergePatch([preset("default")], []),
    /reserved/,
  )
  assert.throws(
    () =>
      buildAgentPresetsMergePatch(
        [preset("Coding"), preset("coding", { id: "duplicate" })],
        [],
      ),
    /unique/,
  )
})
