import {
  IconHistory,
  IconPencil,
  IconStar,
  IconTrash,
} from "@tabler/icons-react"
import dayjs from "dayjs"
import type { PointerEvent as ReactPointerEvent, RefObject } from "react"
import { useEffect, useRef, useState } from "react"
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
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import { ScrollArea } from "@/components/ui/scroll-area"
import { cn } from "@/lib/utils"

const ACTIONS_WIDTH = 132
const SWIPE_AXIS_LOCK = 8
const SWIPE_SETTLE_THRESHOLD = 40

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

interface SessionHistoryItemProps {
  session: SessionSummary
  active: boolean
  swipeEnabled: boolean
  revealedSessionId: string | null
  editingSessionId: string | null
  editingTitle: string
  confirmingDeleteId: string | null
  renameInputRef: RefObject<HTMLInputElement | null>
  onReveal: (sessionId: string | null) => void
  onSwitchSession: (sessionId: string) => void
  onDeleteSession: (sessionId: string) => void
  onToggleFavorite: (sessionId: string, currentlyFavorited: boolean) => void
  onRenameSession: (sessionId: string, title: string) => void
  onSetEditingSession: (sessionId: string | null) => void
  onSetEditingTitle: (title: string) => void
  onSetConfirmingDelete: (sessionId: string | null) => void
}

type SwipeAxis = "pending" | "horizontal" | "vertical"

interface SwipeGesture {
  pointerId: number
  startX: number
  startY: number
  startOffset: number
  offset: number
  axis: SwipeAxis
}

function useSwipeActionMode() {
  const [enabled, setEnabled] = useState(false)

  useEffect(() => {
    const mediaQuery = window.matchMedia("(hover: none) and (pointer: coarse)")
    const update = () => setEnabled(mediaQuery.matches)

    update()
    mediaQuery.addEventListener("change", update)
    return () => mediaQuery.removeEventListener("change", update)
  }, [])

  return enabled
}

function SessionHistoryItem({
  session,
  active,
  swipeEnabled,
  revealedSessionId,
  editingSessionId,
  editingTitle,
  confirmingDeleteId,
  renameInputRef,
  onReveal,
  onSwitchSession,
  onDeleteSession,
  onToggleFavorite,
  onRenameSession,
  onSetEditingSession,
  onSetEditingTitle,
  onSetConfirmingDelete,
}: SessionHistoryItemProps) {
  const { t } = useTranslation()
  const gestureRef = useRef<SwipeGesture | null>(null)
  const suppressClickRef = useRef(false)
  const [dragOffset, setDragOffset] = useState(0)
  const [isDragging, setIsDragging] = useState(false)

  const revealed = revealedSessionId === session.id
  const settledOffset = revealed ? ACTIONS_WIDTH : 0
  const currentOffset = isDragging ? dragOffset : settledOffset

  const markGestureClickSuppressed = () => {
    suppressClickRef.current = true
  }

  const clearGesture = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId)
    }
    gestureRef.current = null
    setIsDragging(false)
  }

  const handlePointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (
      !swipeEnabled ||
      (event.pointerType !== "touch" && event.pointerType !== "pen") ||
      !event.isPrimary ||
      editingSessionId === session.id ||
      confirmingDeleteId === session.id
    ) {
      return
    }

    if (suppressClickRef.current) {
      suppressClickRef.current = false
    }

    const startOffset = revealed ? ACTIONS_WIDTH : 0
    if (!revealedSessionId || revealedSessionId !== session.id) {
      onReveal(null)
    }

    gestureRef.current = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      startOffset,
      offset: startOffset,
      axis: "pending",
    }
    setDragOffset(startOffset)
  }

  const handlePointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    const gesture = gestureRef.current
    if (!gesture || gesture.pointerId !== event.pointerId) return

    const deltaX = event.clientX - gesture.startX
    const deltaY = event.clientY - gesture.startY

    if (gesture.axis === "pending") {
      if (Math.max(Math.abs(deltaX), Math.abs(deltaY)) < SWIPE_AXIS_LOCK) {
        return
      }

      if (Math.abs(deltaY) >= Math.abs(deltaX)) {
        gesture.axis = "vertical"
        markGestureClickSuppressed()
        return
      }

      gesture.axis = "horizontal"
      setIsDragging(true)
      event.currentTarget.setPointerCapture(event.pointerId)
    }

    if (gesture.axis !== "horizontal") return

    event.preventDefault()
    const nextOffset = Math.min(
      ACTIONS_WIDTH,
      Math.max(0, gesture.startOffset - deltaX),
    )
    gesture.offset = nextOffset
    setDragOffset(nextOffset)
  }

  const handlePointerUp = (event: ReactPointerEvent<HTMLDivElement>) => {
    const gesture = gestureRef.current
    if (!gesture || gesture.pointerId !== event.pointerId) return

    if (gesture.axis === "horizontal") {
      event.preventDefault()
      markGestureClickSuppressed()

      const shouldReveal =
        gesture.startOffset === 0
          ? gesture.offset >= SWIPE_SETTLE_THRESHOLD
          : gesture.offset > ACTIONS_WIDTH - SWIPE_SETTLE_THRESHOLD
      onReveal(shouldReveal ? session.id : null)
    } else if (gesture.axis === "vertical") {
      markGestureClickSuppressed()
    }

    clearGesture(event)
  }

  const handlePointerCancel = (event: ReactPointerEvent<HTMLDivElement>) => {
    const gesture = gestureRef.current
    if (!gesture || gesture.pointerId !== event.pointerId) return

    if (gesture.axis === "horizontal") {
      markGestureClickSuppressed()
      onReveal(gesture.startOffset > 0 ? session.id : null)
    }
    clearGesture(event)
  }

  const handleItemClick = (event: React.MouseEvent<HTMLDivElement>) => {
    if (suppressClickRef.current) {
      suppressClickRef.current = false
      event.preventDefault()
      event.stopPropagation()
      return
    }
    if (editingSessionId === session.id) return

    onReveal(null)
    onSwitchSession(session.id)
  }

  const renderActions = (mobile: boolean) => {
    const mobileActionClass =
      "h-full w-11 shrink-0 rounded-none border-l border-border/60"
    const desktopActionClass = "absolute top-1/2 h-6 w-6 -translate-y-1/2"
    const actionTabIndex = mobile ? (revealed ? 0 : -1) : undefined

    return (
      <>
        <Button
          variant="ghost"
          size="icon"
          tabIndex={actionTabIndex}
          aria-label={t("chat.renameSession")}
          className={cn(
            "text-muted-foreground hover:text-muted-foreground",
            mobile
              ? mobileActionClass
              : `${desktopActionClass} right-16 opacity-0 transition-opacity group-hover:opacity-100`,
          )}
          onClick={(event) => {
            event.preventDefault()
            event.stopPropagation()
            onReveal(null)
            onSetEditingSession(session.id)
            onSetEditingTitle(session.title)
          }}
        >
          <IconPencil className="h-3.5 w-3.5" />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          tabIndex={actionTabIndex}
          aria-label={
            session.is_favorited
              ? t("chat.unfavoriteSession")
              : t("chat.favoriteSession")
          }
          className={cn(
            "text-muted-foreground hover:text-muted-foreground",
            mobile
              ? mobileActionClass
              : `${desktopActionClass} right-9 transition-opacity ${session.is_favorited ? "opacity-100" : "opacity-0 group-hover:opacity-100"}`,
          )}
          onClick={(event) => {
            event.preventDefault()
            event.stopPropagation()
            onReveal(null)
            onToggleFavorite(session.id, session.is_favorited)
          }}
        >
          <IconStar
            className="h-4 w-4"
            fill={session.is_favorited ? "currentColor" : "none"}
          />
        </Button>
        <Popover
          open={confirmingDeleteId === session.id}
          modal={true}
          onOpenChange={(open) => {
            if (!open) onSetConfirmingDelete(null)
          }}
        >
          <PopoverTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              tabIndex={actionTabIndex}
              aria-label={t("chat.deleteSession")}
              className={cn(
                mobile
                  ? `${mobileActionClass} bg-destructive/10 text-destructive hover:bg-destructive/20 hover:text-destructive`
                  : `text-muted-foreground hover:bg-destructive/10 hover:text-destructive ${desktopActionClass} right-2 opacity-0 transition-opacity group-hover:opacity-100`,
              )}
              onClick={(event) => {
                event.preventDefault()
                event.stopPropagation()
                onSetConfirmingDelete(session.id)
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
            onClick={(event) => event.stopPropagation()}
          >
            <p className="mb-3 text-sm leading-relaxed">
              {t("chat.deleteSessionConfirm")}
            </p>
            <div className="flex justify-end gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={(event) => {
                  event.stopPropagation()
                  onSetConfirmingDelete(null)
                }}
              >
                {t("chat.deleteSessionCancel")}
              </Button>
              <Button
                variant="destructive"
                size="sm"
                onClick={(event) => {
                  event.stopPropagation()
                  onReveal(null)
                  onDeleteSession(session.id)
                  onSetConfirmingDelete(null)
                }}
              >
                {t("chat.deleteSessionConfirmButton")}
              </Button>
            </div>
          </PopoverContent>
        </Popover>
      </>
    )
  }

  return (
    <DropdownMenuItem
      className={cn(
        "group relative my-0.5",
        swipeEnabled
          ? "block overflow-hidden p-0"
          : "flex flex-col items-start gap-0.5 pr-14",
        !swipeEnabled && active && "bg-accent",
      )}
      onClick={handleItemClick}
      onSelect={(event) => {
        if (swipeEnabled && suppressClickRef.current) {
          event.preventDefault()
        }
      }}
    >
      {swipeEnabled && (
        <div
          aria-hidden={!revealed}
          className={cn(
            "bg-muted/50 absolute inset-y-0 right-0 z-0 flex w-[132px] items-stretch",
            revealed || isDragging ? "visible" : "invisible",
            revealed && !isDragging
              ? "pointer-events-auto"
              : "pointer-events-none",
          )}
        >
          {renderActions(true)}
        </div>
      )}

      <div
        className={cn(
          swipeEnabled
            ? "bg-popover group-focus:bg-accent relative z-10 flex min-h-12 w-full touch-pan-y flex-col items-start gap-0.5 px-2 py-1.5 transition-transform duration-200 ease-out select-none"
            : "contents",
          swipeEnabled && isDragging && "transition-none",
          swipeEnabled && active && "bg-accent",
        )}
        style={
          swipeEnabled
            ? { transform: `translate3d(-${currentOffset}px, 0, 0)` }
            : undefined
        }
        onPointerDown={swipeEnabled ? handlePointerDown : undefined}
        onPointerMove={swipeEnabled ? handlePointerMove : undefined}
        onPointerUp={swipeEnabled ? handlePointerUp : undefined}
        onPointerCancel={swipeEnabled ? handlePointerCancel : undefined}
      >
        {editingSessionId === session.id ? (
          <div
            className="flex w-full items-center gap-1"
            onClick={(event) => event.stopPropagation()}
          >
            <Input
              ref={renameInputRef}
              value={editingTitle}
              placeholder="Enter确认，Esc取消"
              onChange={(event) => onSetEditingTitle(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault()
                  const trimmed = editingTitle.trim()
                  if (trimmed) onRenameSession(session.id, trimmed)
                  onSetEditingSession(null)
                } else if (event.key === "Escape") {
                  event.preventDefault()
                  onSetEditingSession(null)
                }
              }}
              className="h-7 text-sm"
              autoFocus
            />
          </div>
        ) : swipeEnabled ? (
          <div className="flex w-full min-w-0 items-center gap-1">
            {session.is_favorited && (
              <IconStar
                aria-hidden="true"
                className="size-3.5 shrink-0 text-amber-500"
                fill="currentColor"
              />
            )}
            <span className="line-clamp-1 min-w-0 text-sm font-medium">
              {session.title}
            </span>
          </div>
        ) : (
          <span className="line-clamp-1 text-sm font-medium">
            {session.title}
          </span>
        )}
        <span className="text-muted-foreground text-xs">
          {t("chat.messagesCount", { count: session.message_count })} ·{" "}
          {dayjs(session.updated).fromNow()}
        </span>
      </div>

      {!swipeEnabled && renderActions(false)}
    </DropdownMenuItem>
  )
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
  const swipeEnabled = useSwipeActionMode()
  const [revealedSessionId, setRevealedSessionId] = useState<string | null>(
    null,
  )
  const [confirmingDeleteId, setConfirmingDeleteId] = useState<string | null>(
    null,
  )
  const [editingSessionId, setEditingSessionId] = useState<string | null>(null)
  const [editingTitle, setEditingTitle] = useState("")
  const renameInputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    setRevealedSessionId(null)
  }, [swipeEnabled])

  const handleOpenChange = (open: boolean) => {
    if (!open) {
      setRevealedSessionId(null)
      setConfirmingDeleteId(null)
      setEditingSessionId(null)
    }
    onOpenChange(open)
  }

  return (
    <DropdownMenu onOpenChange={handleOpenChange}>
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
              <SessionHistoryItem
                key={session.id}
                session={session}
                active={session.id === activeSessionId}
                swipeEnabled={swipeEnabled}
                revealedSessionId={revealedSessionId}
                editingSessionId={editingSessionId}
                editingTitle={editingTitle}
                confirmingDeleteId={confirmingDeleteId}
                renameInputRef={renameInputRef}
                onReveal={setRevealedSessionId}
                onSwitchSession={onSwitchSession}
                onDeleteSession={onDeleteSession}
                onToggleFavorite={onToggleFavorite}
                onRenameSession={onRenameSession}
                onSetEditingSession={setEditingSessionId}
                onSetEditingTitle={setEditingTitle}
                onSetConfirmingDelete={setConfirmingDeleteId}
              />
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
