import { launcherFetch } from "@/api/http"

export interface SessionSummary {
  id: string
  title: string
  preview: string
  message_count: number
  created: string
  updated: string
  is_favorited: boolean
}

export interface SessionDetailMessage {
  role: "user" | "assistant"
  content: string
  created_at?: string
  kind?: "normal" | "thought" | "tool_calls"
  model_name?: string
  media?: string[]
  attachments?: {
    type?: "image" | "audio" | "video" | "file"
    url: string
    filename?: string
    content_type?: string
  }[]
  tool_calls?: {
    id?: string
    type?: string
    function?: {
      name?: string
      arguments?: string
    }
    extra_content?: {
      tool_feedback_explanation?: string
    }
  }[]
}

export interface SessionDetail {
  id: string
  messages: SessionDetailMessage[]
  summary: string
  agent_preset?: string
  effective_model?: string
  archived_count?: number
  created: string
  updated: string
}

export async function getSessions(
  offset: number = 0,
  limit: number = 20,
): Promise<SessionSummary[]> {
  const params = new URLSearchParams({
    offset: offset.toString(),
    limit: limit.toString(),
  })

  const res = await launcherFetch(`/api/sessions?${params.toString()}`)
  if (!res.ok) {
    throw new Error(`Failed to fetch sessions: ${res.status}`)
  }
  return res.json()
}

export async function getSessionHistory(id: string): Promise<SessionDetail> {
  const res = await launcherFetch(`/api/sessions/${encodeURIComponent(id)}`)
  if (!res.ok) {
    throw new Error(`Failed to fetch session ${id}: ${res.status}`)
  }
  return res.json()
}

export async function deleteSession(id: string): Promise<void> {
  const res = await launcherFetch(`/api/sessions/${encodeURIComponent(id)}`, {
    method: "DELETE",
  })
  if (!res.ok) {
    throw new Error(`Failed to delete session ${id}: ${res.status}`)
  }
}

export async function favoriteSession(id: string): Promise<void> {
  const res = await launcherFetch(
    `/api/sessions/${encodeURIComponent(id)}/favorite`,
    {
      method: "POST",
    },
  )
  if (!res.ok) {
    throw new Error(`Failed to favorite session ${id}: ${res.status}`)
  }
}

export async function unfavoriteSession(id: string): Promise<void> {
  const res = await launcherFetch(
    `/api/sessions/${encodeURIComponent(id)}/favorite`,
    {
      method: "DELETE",
    },
  )
  if (!res.ok) {
    throw new Error(`Failed to unfavorite session ${id}: ${res.status}`)
  }
}

export async function renameSession(
  id: string,
  title: string,
): Promise<{ id: string; title: string }> {
  const res = await launcherFetch(
    `/api/sessions/${encodeURIComponent(id)}/rename`,
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ title }),
    },
  )
  if (!res.ok) {
    throw new Error(`Failed to rename session ${id}: ${res.status}`)
  }
  return res.json()
}

export interface ForkSessionRequest {
  new_session_id: string
  transcript_index: number
}

export interface ForkSessionResponse {
  source_session_id: string
  new_session_id: string
}

export async function forkSession(
  id: string,
  newSessionId: string,
  transcriptIndex: number,
): Promise<ForkSessionResponse> {
  const res = await launcherFetch(
    `/api/sessions/${encodeURIComponent(id)}/fork`,
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        new_session_id: newSessionId,
        transcript_index: transcriptIndex,
      }),
    },
  )
  if (!res.ok) {
    throw new Error(`Failed to fork session ${id}: ${res.status}`)
  }
  return res.json()
}

export async function deleteMessageSeries(
  id: string,
  transcriptIndex: number,
): Promise<SessionDetail> {
  const params = new URLSearchParams({
    transcript_index: transcriptIndex.toString(),
  })
  const res = await launcherFetch(
    `/api/sessions/${encodeURIComponent(id)}/message-series?${params.toString()}`,
    { method: "DELETE" },
  )
  if (!res.ok) {
    throw new Error(
      `Failed to delete a message series from session ${id}: ${res.status}`,
    )
  }
  return res.json()
}
