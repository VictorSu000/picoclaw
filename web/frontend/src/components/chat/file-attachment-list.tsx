import { IconDownload, IconFileText } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import { cn } from "@/lib/utils"
import type { ChatAttachment } from "@/store/chat"

interface FileAttachmentListProps {
  attachments: ChatAttachment[]
  align?: "start" | "end"
  compact?: boolean
}

function formatAttachmentSize(size?: number): string {
  if (!size || size <= 0) return ""
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${Math.ceil(size / 1024)} KB`
  return `${(size / (1024 * 1024)).toFixed(1)} MB`
}

export function FileAttachmentList({
  attachments,
  align = "start",
  compact = false,
}: FileAttachmentListProps) {
  const { t } = useTranslation()
  const files = attachments.filter(
    (attachment) => attachment.type !== "image",
  )

  if (files.length === 0) return null

  return (
    <div
      className={cn(
        "flex flex-wrap gap-2",
        align === "end" && "justify-end",
      )}
    >
      {files.map((attachment, index) => {
        const extension = attachment.filename
          ?.split(".")
          .pop()
          ?.toUpperCase()
        const size = formatAttachmentSize(attachment.size)

        return (
          <a
            key={`${attachment.url}-${index}`}
            href={attachment.url}
            download={attachment.filename}
            className={cn(
              "group/file border-border/60 bg-card flex w-fit max-w-sm items-center rounded-xl border transition-all duration-200 hover:-translate-y-0.5 hover:border-violet-500/30 hover:shadow-sm dark:hover:border-violet-500/40",
              compact
                ? "min-w-[180px] gap-2.5 px-3 py-2"
                : "min-w-[220px] gap-3.5 px-4 py-3",
            )}
          >
            <div
              className={cn(
                "flex shrink-0 items-center justify-center rounded-lg text-violet-400 ring-1 ring-violet-500/10 dark:bg-violet-500/10 dark:ring-violet-500/30",
                compact ? "h-8 w-8" : "h-10 w-10",
              )}
            >
              <IconFileText className={compact ? "h-4 w-4" : "h-5 w-5"} />
            </div>
            <div className="flex min-w-0 flex-1 flex-col pr-1">
              <span className="text-foreground/90 truncate text-[14px] leading-tight font-medium transition-colors group-hover/file:text-violet-600 dark:group-hover/file:text-violet-400">
                {attachment.filename || t("chat.downloadFile")}
              </span>
              <span className="text-muted-foreground/70 mt-1 text-[11px] font-medium">
                {[extension || "FILE", size].filter(Boolean).join(" · ")}
              </span>
            </div>
            <div className="bg-muted/60 text-muted-foreground/50 dark:bg-muted/20 flex h-8 w-8 shrink-0 items-center justify-center rounded-full transition-all duration-200 group-hover/file:bg-violet-400 group-hover/file:text-white dark:group-hover/file:bg-violet-400">
              <IconDownload className="h-4 w-4" />
            </div>
          </a>
        )
      })}
    </div>
  )
}
