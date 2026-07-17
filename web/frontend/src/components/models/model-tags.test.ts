/// <reference types="node" />
import assert from "node:assert/strict"
import test from "node:test"

import { parseModelTags } from "./model-tags.ts"

test("parseModelTags normalizes and deduplicates comma-separated tags", () => {
  assert.deepEqual(
    parseModelTags(" Vision, tools  ,VISION, image-understanding "),
    ["vision", "tools", "image-understanding"],
  )
})

test("parseModelTags omits empty values", () => {
  assert.deepEqual(parseModelTags(" ,  ,\n"), [])
})
