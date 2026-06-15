import { IconHistory, IconTrash, IconStar, IconPencil } from "@tabler/icons-react"
import dayjs from "dayjs"
import type { RefObject } from "react"
import { useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import type { SessionSummary } from "@/api/sessions"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { ScrollArea } from "@/components/ui/scroll-area"

interface SessionHistoryMenuProps {
  sessions: SessionSummary[]
  activeSessionId: string
  hasMore: boolean
  loadError: boolean
  loadErrorMessage: string
  observerRef: RefObject<HTMLDivElement | null>
  onOpenChange: (open: boolean) => void
  onSwitchSession: (sessionId: string) => void
  onDeleteSession: (sessionId: string) => void
  onToggleFavorite: (sessionId: string, currentlyFavorited: boolean) => void
  onRenameSession: (sessionId: string, title: string) => void
}

export function SessionHistoryMenu({
  sessions,
  activeSessionId,
  hasMore,
  loadError,
  loadErrorMessage,
  observerRef,
  onOpenChange,
  onSwitchSession,
  onDeleteSession,
  onToggleFavorite,
  onRenameSession,
}: SessionHistoryMenuProps) {
  const { t } = useTranslation()
  const [confirmingDeleteId, setConfirmingDeleteId] = useState<string | null>(null)
  const [editingSessionId, setEditingSessionId] = useState<string | null>(null)
  const [editingTitle, setEditingTitle] = useState("")
  const renameInputRef = useRef<HTMLInputElement>(null)

  return (
    <DropdownMenu onOpenChange={onOpenChange}>
      <DropdownMenuTrigger asChild>
        <Button variant="secondary" size="sm" className="h-9 gap-2">
          <IconHistory className="size-4" />
          <span className="hidden sm:inline">{t("chat.history")}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-72">
        <ScrollArea className="max-h-[300px]">
          {loadError && (
            <DropdownMenuItem disabled>
              <span className="text-destructive text-xs">
                {loadErrorMessage}
              </span>
            </DropdownMenuItem>
          )}
          {sessions.length === 0 && !loadError ? (
            <DropdownMenuItem disabled>
              <span className="text-muted-foreground text-xs">
                {t("chat.noHistory")}
              </span>
            </DropdownMenuItem>
          ) : (
            sessions.map((session) => (
              <DropdownMenuItem
                key={session.id}
                className={`group relative my-0.5 flex flex-col items-start gap-0.5 pr-14 ${session.id === activeSessionId ? "bg-accent" : ""
                  }`}
                onClick={() => {
                  if (editingSessionId !== session.id) {
                    onSwitchSession(session.id)
                  }
                }}
              >
                {editingSessionId === session.id ? (
                  <div
                    className="flex w-full items-center gap-1"
                    onClick={(e) => e.stopPropagation()}
                  >
                    <Input
                      ref={renameInputRef}
                      value={editingTitle}
                      placeholder="Enter确认，Esc取消"
                      onChange={(e) => setEditingTitle(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") {
                          e.preventDefault()
                          const trimmed = editingTitle.trim()
                          if (trimmed) {
                            onRenameSession(session.id, trimmed)
                          }
                          setEditingSessionId(null)
                        } else if (e.key === "Escape") {
                          e.preventDefault()
                          setEditingSessionId(null)
                        }
                      }}
                      className="h-7 text-sm"
                      autoFocus
                    />
                  </div>
                ) : (
                  <span className="line-clamp-1 text-sm font-medium">
                    {session.title}
                  </span>
                )}
                <span className="text-muted-foreground text-xs">
                  {t("chat.messagesCount", {
                    count: session.message_count,
                  })}{" "}
                  · {dayjs(session.updated).fromNow()}
                </span>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={t("chat.renameSession")}
                  className="text-muted-foreground hover:text-muted-foreground absolute top-1/2 right-16 h-6 w-6 -translate-y-1/2 opacity-0 transition-opacity group-hover:opacity-100"
                  onClick={(e) => {
                    e.preventDefault()
                    e.stopPropagation()
                    setEditingSessionId(session.id)
                    setEditingTitle(session.title)
                    setTimeout(() => renameInputRef.current?.focus(), 0)
                  }}
                >
                  <IconPencil className="h-3.5 w-3.5" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={session.is_favorited ? t("chat.unfavoriteSession") : t("chat.favoriteSession")}
                  className={`absolute top-1/2 right-9 h-6 w-6 -translate-y-1/2 transition-opacity ${session.is_favorited
                    ? "opacity-100 text-muted-foreground hover:text-muted-foreground"
                    : "opacity-0 group-hover:opacity-100 text-muted-foreground hover:text-muted-foreground"
                    }`}
                  onClick={(e) => {
                    e.preventDefault()
                    e.stopPropagation()
                    onToggleFavorite(session.id, session.is_favorited)
                  }}
                >
                  <IconStar className="h-4 w-4" fill={session.is_favorited ? "currentColor" : "none"} />
                </Button>
                <Popover
                  open={confirmingDeleteId === session.id}
                  modal={true}
                  onOpenChange={(open) => {
                    if (!open) setConfirmingDeleteId(null)
                  }}
                >
                  <PopoverTrigger asChild>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={t("chat.deleteSession")}
                      className="text-muted-foreground hover:bg-destructive/10 hover:text-destructive absolute top-1/2 right-2 h-6 w-6 -translate-y-1/2 opacity-0 transition-opacity group-hover:opacity-100"
                      onClick={(e) => {
                        e.preventDefault()
                        e.stopPropagation()
                        setConfirmingDeleteId(session.id)
                      }}
                    >
                      <IconTrash className="h-4 w-4" />
                    </Button>
                  </PopoverTrigger>
                  <PopoverContent
                    align="end"
                    side="left"
                    sideOffset={8}
                    className="w-56 p-3"
                    onClick={(e) => e.stopPropagation()}
                  >
                    <p className="text-sm leading-relaxed mb-3">
                      {t("chat.deleteSessionConfirm")}
                    </p>
                    <div className="flex justify-end gap-2">
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={(e) => {
                          e.stopPropagation()
                          setConfirmingDeleteId(null)
                        }}
                      >
                        {t("chat.deleteSessionCancel")}
                      </Button>
                      <Button
                        variant="destructive"
                        size="sm"
                        onClick={(e) => {
                          e.stopPropagation()
                          onDeleteSession(session.id)
                          setConfirmingDeleteId(null)
                        }}
                      >
                        {t("chat.deleteSessionConfirmButton")}
                      </Button>
                    </div>
                  </PopoverContent>
                </Popover>
              </DropdownMenuItem>
            ))
          )}
          {hasMore && sessions.length > 0 && (
            <div ref={observerRef} className="py-2 text-center">
              <span className="text-muted-foreground animate-pulse text-xs">
                {t("chat.loadingMore")}
              </span>
            </div>
          )}
        </ScrollArea>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
