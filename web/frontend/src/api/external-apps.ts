import { launcherFetch } from "./http"

export interface ExternalApp {
  id: string
  name: string
  icon?: string
}

/**
 * Fetch the list of configured external applications
 */
export async function getExternalApps(): Promise<ExternalApp[]> {
  const response = await launcherFetch("/api/launcher/external-apps")
  if (!response.ok) {
    throw new Error(`Failed to fetch external apps: ${response.statusText}`)
  }
  return response.json()
}
