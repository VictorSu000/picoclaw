export function parseModelTags(value: string): string[] {
  const seen = new Set<string>()
  const tags: string[] = []
  for (const rawTag of value.split(/[\s,]+/)) {
    const tag = rawTag.trim().toLowerCase()
    if (!tag || seen.has(tag)) continue
    seen.add(tag)
    tags.push(tag)
  }
  return tags
}
