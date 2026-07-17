import { launcherFetch } from "@/api/http"
import type { ChatAttachment } from "@/store/chat"

interface UploadResponse {
  type: "image" | "audio" | "video" | "file"
  url: string
  filename: string
  content_type: string
  size: number
}

export async function uploadChatFile(
  file: File,
  sessionId: string,
): Promise<ChatAttachment> {
  const body = new FormData()
  body.append("session_id", sessionId)
  body.append("file", file, file.name)

  const response = await launcherFetch("/pico/media", {
    method: "POST",
    body,
  })
  if (!response.ok) {
    if (response.status === 413) {
      throw new Error("file_too_large")
    }
    throw new Error(`upload_failed:${response.status}`)
  }

  const uploaded = (await response.json()) as UploadResponse
  return {
    type: uploaded.type,
    url: uploaded.url,
    filename: uploaded.filename,
    contentType: uploaded.content_type,
    size: uploaded.size,
    uploadSessionId: sessionId,
  }
}

export async function deleteChatFile(
  attachmentUrl: string,
  sessionId: string,
): Promise<void> {
  if (!attachmentUrl.startsWith("/pico/media/") || !sessionId) {
    return
  }
  const separator = attachmentUrl.includes("?") ? "&" : "?"
  await launcherFetch(
    `${attachmentUrl}${separator}session_id=${encodeURIComponent(sessionId)}`,
    { method: "DELETE" },
  )
}
