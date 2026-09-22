import { launcherFetch } from "@/api/http"

export const DEFAULT_CATEGORY_ID = "default"

export interface Category {
  id: string
  /** Empty for the default category — render via i18n. */
  name: string
  created: string
}

export async function getCategories(): Promise<Category[]> {
  const res = await launcherFetch("/api/categories")
  if (!res.ok) {
    throw new Error(`Failed to fetch categories: ${res.status}`)
  }
  return res.json()
}

export async function createCategory(name: string): Promise<Category> {
  const res = await launcherFetch("/api/categories", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ name }),
  })
  if (!res.ok) {
    if (res.status === 409) {
      throw new Error("duplicate")
    }
    throw new Error(`Failed to create category: ${res.status}`)
  }
  return res.json()
}
