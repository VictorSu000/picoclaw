import { launcherFetch } from "@/api/http"

export interface AgentPresetListItem {
  name: string
  effective_model: string
  fallbacks?: string[]
  model_overridden: boolean
}

export interface AgentPresetCatalogResponse {
  default_model: string
  default_preset: string
  presets: AgentPresetListItem[]
}

export interface SessionAgentPresetResponse {
  agent_preset: string
  agent_preset_override: boolean
  effective_model: string
  fallbacks?: string[]
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await launcherFetch(path, options)
  if (!response.ok) {
    const detail = await response.text().catch(() => "")
    throw new Error(detail.trim() || `API error: ${response.status}`)
  }
  return response.json() as Promise<T>
}

export function getAgentPresets(
  sessionID: string,
): Promise<AgentPresetCatalogResponse> {
  const params = new URLSearchParams({ session_id: sessionID })
  return request<AgentPresetCatalogResponse>(
    `/api/agent-presets?${params.toString()}`,
  )
}

export function setSessionAgentPreset(
  sessionID: string,
  presetName: string,
): Promise<SessionAgentPresetResponse> {
  return request<SessionAgentPresetResponse>(
    `/api/sessions/${encodeURIComponent(sessionID)}/preset`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ agent_preset: presetName }),
    },
  )
}
