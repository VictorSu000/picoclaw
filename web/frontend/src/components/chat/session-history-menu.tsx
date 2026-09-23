import {
  IconHistory,
  IconPencil,
  IconStar,
  IconTrash,
  IconFolder,
} from "@tabler/icons-react"
import dayjs from "dayjs"
import type { PointerEvent as ReactPointerEvent, RefObject } from "react"
import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import { DEFAULT_CATEGORY_ID, type Category } from "@/api/categories"
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

const ACTIONS_WIDTH = 176
const SWIPE_AXIS_LOCK = 8
const SWIPE_SETTLE_THRESHOLD = 40

interface SessionHistoryMenuProps {
  sessions: SessionSummary[]
  activeSessionId: string
  hasMore: boolean
  loadError: boolean
  loadErrorMessage: string
  observerRef: RefObject<HTMLDivElement | null>
  categories: Category[]
  onOpenChange: (open: boolean) => void
  onSwitchSession: (sessionId: string) => void
  onDeleteSession: (sessionId: string) => void
  onToggleFavorite: (sessionId: string, currentlyFavorited: boolean) => void
  onRenameSession: (sessionId: string, title: string) => void
  onMoveSession: (sessionId: string, categoryId: string) => void | Promise<void>
}

interface SessionHistoryItemProps {
  session: SessionSummary
  active: boolean
  swipeEnabled: boolean
  revealedSessionId: string | null
  editingSessionId: string | null
  editingTitle: string
  confirmingDeleteId: string | null
  movingSessionId: string | null
  renameInputRef: RefObject<HTMLInputElement | null>
  categories: Category[]
  onReveal: (sessionId: string | null) => void
  onSwitchSession: (sessionId: string) => void
  onDeleteSession: (sessionId: string) => void
  onToggleFavorite: (sessionId: string, currentlyFavorited: boolean) => void
  onRenameSession: (sessionId: string, title: string) => void
  onMoveSession: (sessionId: string, categoryId: string) => void
  onSetEditingSession: (sessionId: string | null) => void
  onSetEditingTitle: (title: string) => void
  onSetConfirmingDelete: (sessionId: string | null) => void
  onSetMovingSession: (sessionId: string | null) => void
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
  movingSessionId,
  renameInputRef,
  categories,
  onReveal,
  onSwitchSession,
  onDeleteSession,
  onToggleFavorite,
  onRenameSession,
  onMoveSession,
  onSetEditingSession,
  onSetEditingTitle,
  onSetConfirmingDelete,
  onSetMovingSession,
}: SessionHistoryItemProps) {
  const { t } = useTranslation()
  const gestureRef = useRef<SwipeGesture | null>(null)
  const suppressClickRef = useRef(false)
  const [dragOffset, setDragOffset] = useState(0)
  const [isDragging, setIsDragging] = useState(false)

  const revealed = revealedSessionId === session.id
  const settledOffset = revealed ? ACTIONS_WIDTH : 0
  const currentOffset = isDragging ? dragOffset : settledOffset
  const sessionCategory = session.category?.trim() || DEFAULT_CATEGORY_ID

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
    const desktopActionClass = "h-6 w-6 shrink-0"
    const actionClass = cn(
      "text-muted-foreground hover:text-muted-foreground",
      mobile ? mobileActionClass : desktopActionClass,
    )
    const actionTabIndex = mobile ? (revealed ? 0 : -1) : undefined

    return (
      <>
        <Popover
          open={movingSessionId === session.id}
          modal={true}
          onOpenChange={(open) => {
            if (!open) onSetMovingSession(null)
          }}
        >
          <PopoverTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              tabIndex={actionTabIndex}
              aria-label={t("categories.moveCategory")}
              className={actionClass}
              onClick={(event) => {
                event.preventDefault()
                event.stopPropagation()
                onSetMovingSession(session.id)
              }}
            >
              <IconFolder className="h-3.5 w-3.5" />
            </Button>
          </PopoverTrigger>
          <PopoverContent
            align="end"
            side="left"
            sideOffset={8}
            className="w-48 p-1"
            onClick={(event) => event.stopPropagation()}
          >
            {categories.map((cat) => {
              const label =
                cat.id === DEFAULT_CATEGORY_ID
                  ? t("categories.default")
                  : cat.name
              const isCurrent = cat.id === sessionCategory
              return (
                <button
                  key={cat.id}
                  type="button"
                  disabled={isCurrent}
                  className="hover:bg-accent w-full rounded-sm px-2 py-1.5 text-left text-sm disabled:opacity-50"
                  onClick={(event) => {
                    event.stopPropagation()
                    onReveal(null)
                    onSetMovingSession(null)
                    onMoveSession(session.id, cat.id)
                  }}
                >
                  {label}
                </button>
              )
            })}
          </PopoverContent>
        </Popover>
        <Button
          variant="ghost"
          size="icon"
          tabIndex={actionTabIndex}
          aria-label={t("chat.renameSession")}
          className={actionClass}
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
          className={actionClass}
          onClick={(event) => {
            event.preventDefault()
            event.stopPropagation()
            onReveal(null)
            onToggleFavorite(session.id, session.is_favorited)
          }}
        >
          <IconStar
            className="h-3.5 w-3.5"
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
                  : `text-muted-foreground hover:bg-destructive/10 hover:text-destructive ${desktopActionClass}`,
              )}
              onClick={(event) => {
                event.preventDefault()
                event.stopPropagation()
                onSetConfirmingDelete(session.id)
              }}
            >
              <IconTrash className="h-3.5 w-3.5" />
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
          : "flex flex-col items-start gap-0.5",
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
            "bg-muted/50 absolute inset-y-0 right-0 z-0 flex w-[176px] items-stretch",
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
        ) : (
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
        )}
        <span className="text-muted-foreground text-xs">
          {t("chat.messagesCount", { count: session.message_count })} ·{" "}
          {dayjs(session.updated).fromNow()}
        </span>
      </div>

      {!swipeEnabled && editingSessionId !== session.id && (
        <div className="bg-popover absolute top-1/2 right-1 z-10 flex -translate-y-1/2 items-center gap-0.5 rounded-md p-0.5 opacity-0 shadow-xs transition-opacity pointer-events-none group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100">
          {renderActions(false)}
        </div>
      )}
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
  categories,
  onOpenChange,
  onSwitchSession,
  onDeleteSession,
  onToggleFavorite,
  onRenameSession,
  onMoveSession,
}: SessionHistoryMenuProps) {
  const { t } = useTranslation()
  const swipeEnabled = useSwipeActionMode()
  const [revealedSessionId, setRevealedSessionId] = useState<string | null>(
    null,
  )
  const [confirmingDeleteId, setConfirmingDeleteId] = useState<string | null>(
    null,
  )
  const [movingSessionId, setMovingSessionId] = useState<string | null>(null)
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
      setMovingSessionId(null)
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
                movingSessionId={movingSessionId}
                renameInputRef={renameInputRef}
                categories={categories}
                onReveal={setRevealedSessionId}
                onSwitchSession={onSwitchSession}
                onDeleteSession={onDeleteSession}
                onToggleFavorite={onToggleFavorite}
                onRenameSession={onRenameSession}
                onMoveSession={onMoveSession}
                onSetEditingSession={setEditingSessionId}
                onSetEditingTitle={setEditingTitle}
                onSetConfirmingDelete={setConfirmingDeleteId}
                onSetMovingSession={setMovingSessionId}
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
