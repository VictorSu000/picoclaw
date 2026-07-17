import {
  IconChevronDown,
  IconChevronUp,
  IconGitFork,
  IconLoader2,
  IconPlus,
  IconTrash,
} from "@tabler/icons-react"
import { useAtom } from "jotai"
import {
  type ChangeEvent,
  type ClipboardEvent,
  type DragEvent,
  useEffect,
  useRef,
  useState,
} from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { AssistantMessage } from "@/components/chat/assistant-message"
import {
  ChatComposer,
  type ChatInputDisabledReason,
} from "@/components/chat/chat-composer"
import { ChatEmptyState } from "@/components/chat/chat-empty-state"
import { ModelSelector } from "@/components/chat/model-selector"
import { PresetSelector } from "@/components/chat/preset-selector"
import { SessionHistoryMenu } from "@/components/chat/session-history-menu"
import { TypingIndicator } from "@/components/chat/typing-indicator"
import { UserMessage } from "@/components/chat/user-message"
import { PageHeader } from "@/components/page-header"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import {
  CHAT_IMAGE_ACCEPT,
  buildChatImageAttachments,
  getTransferredFiles,
  hasFileTransfer,
} from "@/features/chat/image-input"
import { useAgentPresets } from "@/hooks/use-agent-presets"
import { useChatModels } from "@/hooks/use-chat-models"
import { useGateway } from "@/hooks/use-gateway"
import { usePicoChat } from "@/hooks/use-pico-chat"
import { useSessionHistory } from "@/hooks/use-session-history"
import type { AssistantDetailVisibility } from "@/store/chat"
import type { ConnectionState } from "@/store/chat"
import type { ChatAttachment } from "@/store/chat"
import {
  assistantDetailVisibilityAtom,
  shouldShowAssistantMessage,
} from "@/store/chat"
import type { GatewayState } from "@/store/gateway"

function resolveChatInputDisabledReason({
  hasDefaultModel,
  connectionState,
  gatewayState,
}: {
  hasDefaultModel: boolean
  connectionState: ConnectionState
  gatewayState: GatewayState
}): ChatInputDisabledReason | null {
  if (gatewayState === "unknown") {
    return "gatewayUnknown"
  }

  if (gatewayState === "starting") {
    return "gatewayStarting"
  }

  if (gatewayState === "restarting") {
    return "gatewayRestarting"
  }

  if (gatewayState === "stopping") {
    return "gatewayStopping"
  }

  if (gatewayState === "stopped") {
    return "gatewayStopped"
  }

  if (gatewayState === "error") {
    return "gatewayError"
  }

  if (connectionState === "connecting") {
    return "websocketConnecting"
  }

  if (connectionState === "error") {
    return "websocketError"
  }

  if (connectionState === "disconnected") {
    return "websocketDisconnected"
  }

  if (!hasDefaultModel) {
    return "noDefaultModel"
  }

  return null
}

/**
 * CompressedHistoryNotice marks the boundary between compaction-archived
 * (view-only) history and the active conversation. It optionally shows a
 * divider above the active region and a collapsible summary of the compressed
 * history. The archived messages and this summary are never sent back into the
 * LLM context when the conversation continues.
 */
function CompressedHistoryNotice({
  summary,
  showDivider,
}: {
  summary?: string
  showDivider: boolean
}) {
  const { t } = useTranslation()
  const [expanded, setExpanded] = useState(false)
  const hasSummary = Boolean(summary && summary.trim())

  if (!showDivider && !hasSummary) {
    return null
  }

  return (
    <div className="my-4 flex flex-col gap-2">
      {showDivider && (
        <div className="flex items-center gap-3">
          <div className="border-border/60 h-px flex-1 border-t border-dashed" />
          <span className="text-muted-foreground/70 text-xs whitespace-nowrap">
            {t("chat.compressedHistoryDivider")}
          </span>
          <div className="border-border/60 h-px flex-1 border-t border-dashed" />
        </div>
      )}
      {hasSummary && (
        <div className="border-border/60 bg-muted/40 rounded-lg border px-3 py-2">
          <button
            type="button"
            onClick={() => setExpanded((value) => !value)}
            className="text-muted-foreground hover:text-foreground flex w-full items-center justify-between gap-2 text-xs font-medium"
            aria-expanded={expanded}
          >
            <span>{t("chat.contextSummaryLabel")}</span>
            {expanded ? (
              <IconChevronUp className="size-4" />
            ) : (
              <IconChevronDown className="size-4" />
            )}
          </button>
          {expanded && (
            <p className="text-muted-foreground mt-2 text-sm whitespace-pre-wrap">
              {summary}
            </p>
          )}
        </div>
      )}
    </div>
  )
}

export function ChatPage() {
  const { t } = useTranslation()
  const scrollRef = useRef<HTMLDivElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const dragDepthRef = useRef(0)
  const [isAtBottom, setIsAtBottom] = useState(true)
  const [hasScrolled, setHasScrolled] = useState(false)
  const [input, setInput] = useState("")
  const [attachments, setAttachments] = useState<ChatAttachment[]>([])
  const [isDragActive, setIsDragActive] = useState(false)
  const [assistantDetailVisibility, setAssistantDetailVisibility] = useAtom(
    assistantDetailVisibilityAtom,
  )

  const assistantDetailVisibilityOptions: Array<{
    value: AssistantDetailVisibility
    label: string
  }> = [
    { value: "none", label: t("chat.assistantDetailVisibility.none") },
    { value: "thought", label: t("chat.assistantDetailVisibility.thought") },
    {
      value: "tool_calls",
      label: t("chat.assistantDetailVisibility.toolCalls"),
    },
    { value: "all", label: t("chat.assistantDetailVisibility.all") },
  ]

  const {
    messages,
    connectionState,
    isTyping,
    activeSessionId,
    contextUsage,
    sessionSummary,
    archivedMessageCount,
    agentPresetName,
    effectiveModelName: storedEffectiveModelName,
    sendMessage,
    switchSession,
    newChat,
    forkChat,
    deleteMessageSeries,
  } = usePicoChat()

  const { state: gwState } = useGateway()
  const isGatewayRunning = gwState === "running"

  const {
    defaultModelName,
    hasAvailableModels,
    apiKeyModels,
    oauthModels,
    localModels,
    handleSetDefault,
  } = useChatModels({ isConnected: isGatewayRunning })
  const {
    presets,
    isLoading: presetsLoading,
    isChanging: presetChanging,
    isPresetActive,
    effectiveModelName,
    fallbacks: presetFallbacks,
    changePreset,
  } = useAgentPresets({
    activeSessionId,
    agentPresetName,
    storedEffectiveModelName,
  })
  const selectedModelName = isPresetActive
    ? effectiveModelName
    : defaultModelName
  const hasDefaultModel = Boolean(selectedModelName)
  const inputDisabledReason = resolveChatInputDisabledReason({
    hasDefaultModel,
    connectionState,
    gatewayState: gwState,
  })
  const canInput = inputDisabledReason === null

  const {
    sessions,
    hasMore,
    loadError,
    loadErrorMessage,
    observerRef,
    loadSessions,
    handleDeleteSession,
    handleToggleFavorite,
    handleRenameSession,
  } = useSessionHistory({
    activeSessionId,
    onDeletedActiveSession: newChat,
  })

  const syncScrollState = (element: HTMLDivElement) => {
    const { clientHeight, scrollHeight, scrollTop } = element
    setHasScrolled(scrollTop > 0)
    setIsAtBottom(scrollHeight - scrollTop <= clientHeight + 10)
  }

  const handleScroll = (e: React.UIEvent<HTMLDivElement>) => {
    syncScrollState(e.currentTarget)
  }

  useEffect(() => {
    if (scrollRef.current) {
      if (isAtBottom) {
        scrollRef.current.scrollTop = scrollRef.current.scrollHeight
      }
      syncScrollState(scrollRef.current)
    }
  }, [messages, isTyping, isAtBottom])

  const handleSend = () => {
    if ((!input.trim() && attachments.length === 0) || !canInput) return
    if (
      sendMessage({
        content: input,
        attachments,
      })
    ) {
      setInput("")
      setAttachments([])
    }
  }

  const handleAddImages = () => {
    if (!canInput) return
    fileInputRef.current?.click()
  }

  const handleRemoveAttachment = (index: number) => {
    setAttachments((prev) => prev.filter((_, itemIndex) => itemIndex !== index))
  }

  const appendImageFiles = async (files: readonly File[]) => {
    if (!canInput || files.length === 0) {
      return
    }

    const nextAttachments = await buildChatImageAttachments(files, t)
    if (nextAttachments.length === 0) {
      return
    }

    setAttachments((prev) => [...prev, ...nextAttachments])
  }

  const handleImageSelection = async (event: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files ?? [])
    event.target.value = ""

    if (files.length === 0) {
      return
    }

    await appendImageFiles(files)
  }

  const resetDragState = () => {
    dragDepthRef.current = 0
    setIsDragActive(false)
  }

  const handleComposerPaste = async (
    event: ClipboardEvent<HTMLTextAreaElement>,
  ) => {
    const files = getTransferredFiles(event.clipboardData)
    if (files.length === 0) {
      return
    }

    await appendImageFiles(files)
  }

  const handleComposerDragEnter = (event: DragEvent<HTMLDivElement>) => {
    if (!hasFileTransfer(event.dataTransfer)) {
      return
    }

    event.preventDefault()
    if (!canInput) {
      return
    }
    dragDepthRef.current += 1
    setIsDragActive(true)
  }

  const handleComposerDragLeave = (event: DragEvent<HTMLDivElement>) => {
    if (!hasFileTransfer(event.dataTransfer)) {
      return
    }

    event.preventDefault()
    if (!canInput) {
      resetDragState()
      return
    }
    dragDepthRef.current = Math.max(0, dragDepthRef.current - 1)
    if (dragDepthRef.current === 0) {
      setIsDragActive(false)
    }
  }

  const handleComposerDragOver = (event: DragEvent<HTMLDivElement>) => {
    if (!hasFileTransfer(event.dataTransfer)) {
      return
    }

    event.preventDefault()
    event.dataTransfer.dropEffect = canInput ? "copy" : "none"
  }

  const handleComposerDrop = async (event: DragEvent<HTMLDivElement>) => {
    if (!hasFileTransfer(event.dataTransfer)) {
      return
    }

    event.preventDefault()
    const files = getTransferredFiles(event.dataTransfer)
    resetDragState()

    if (!canInput || files.length === 0) {
      return
    }

    await appendImageFiles(files)
  }

  const canSubmit =
    canInput && (Boolean(input.trim()) || attachments.length > 0)

  const [forkingMessageIndex, setForkingMessageIndex] = useState<number | null>(
    null,
  )
  const [deletingMessageIndex, setDeletingMessageIndex] = useState<
    number | null
  >(null)
  const [pendingMessageDelete, setPendingMessageDelete] = useState<{
    visibleIndex: number
    archived: boolean
  } | null>(null)

  const handleForkChat = async (visibleIndex: number) => {
    setForkingMessageIndex(visibleIndex)
    try {
      await forkChat(visibleIndex)
    } catch {
      toast.error(t("chat.forkSessionFailed"))
    } finally {
      setForkingMessageIndex(null)
    }
  }

  const handleDeleteMessageSeries = async () => {
    if (!pendingMessageDelete) return

    const { visibleIndex } = pendingMessageDelete
    setDeletingMessageIndex(visibleIndex)
    try {
      await deleteMessageSeries(visibleIndex)
      setPendingMessageDelete(null)
      toast.success(t("chat.deleteMessageSuccess"))
      await loadSessions(true)
    } catch {
      toast.error(t("chat.deleteMessageFailed"))
    } finally {
      setDeletingMessageIndex(null)
    }
  }

  return (
    <div className="bg-background/95 flex h-full flex-col">
      <PageHeader
        title={t("navigation.chat")}
        className={`transition-shadow ${hasScrolled ? "shadow-xs" : "shadow-none"}`}
        titleExtra={
          (hasAvailableModels || presets.length > 0 || isPresetActive) && (
            <div className="flex min-w-0 items-center gap-1">
              <ModelSelector
                defaultModelName={defaultModelName}
                displayModelName={
                  isPresetActive ? effectiveModelName : undefined
                }
                disabled={isPresetActive}
                disabledReason={
                  isPresetActive
                    ? `${t("chat.modelControlledByPreset", {
                        name: agentPresetName,
                      })}${
                        presetFallbacks.length > 0
                          ? ` ${t("chat.presetFallbacks", {
                              models: presetFallbacks.join(", "),
                            })}`
                          : ""
                      }`
                    : undefined
                }
                apiKeyModels={apiKeyModels}
                oauthModels={oauthModels}
                localModels={localModels}
                onValueChange={handleSetDefault}
              />
              {(presets.length > 0 || isPresetActive || presetsLoading) && (
                <PresetSelector
                  value={agentPresetName}
                  presets={presets}
                  disabled={isTyping || presetChanging || presetsLoading}
                  onValueChange={(value) => void changePreset(value)}
                />
              )}
            </div>
          )
        }
      >
        <div className="border-border/60 hidden items-center gap-2 rounded-lg border px-3 py-1.5 sm:flex">
          <span className="text-muted-foreground text-sm">
            {t("chat.showAssistantDetails")}
          </span>
          <Select
            value={assistantDetailVisibility}
            onValueChange={(value) =>
              setAssistantDetailVisibility(value as AssistantDetailVisibility)
            }
          >
            <SelectTrigger
              size="sm"
              aria-label={t("chat.showAssistantDetails")}
              className="text-muted-foreground hover:text-foreground focus-visible:border-input h-8 min-w-[104px] bg-transparent shadow-none focus-visible:ring-0"
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent align="end">
              {assistantDetailVisibilityOptions.map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {option.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <Button
          variant="secondary"
          size="sm"
          onClick={newChat}
          className="h-9 gap-2"
        >
          <IconPlus className="size-4" />
          <span className="hidden sm:inline">{t("chat.newChat")}</span>
        </Button>

        <SessionHistoryMenu
          sessions={sessions}
          activeSessionId={activeSessionId}
          hasMore={hasMore}
          loadError={loadError}
          loadErrorMessage={loadErrorMessage}
          observerRef={observerRef}
          onOpenChange={(open) => {
            if (open) {
              void loadSessions(true)
            }
          }}
          onSwitchSession={switchSession}
          onDeleteSession={handleDeleteSession}
          onToggleFavorite={handleToggleFavorite}
          onRenameSession={handleRenameSession}
        />
      </PageHeader>

      <div
        ref={scrollRef}
        onScroll={handleScroll}
        className="min-h-0 flex-1 [scrollbar-gutter:stable] overflow-y-auto px-4 py-6 md:px-8 lg:px-24 xl:px-48"
      >
        <div className="mx-auto flex w-full max-w-250 flex-col gap-4 pb-4">
          {messages.length === 0 && !isTyping && (
            <ChatEmptyState
              hasAvailableModels={hasAvailableModels}
              defaultModelName={selectedModelName}
              isConnected={isGatewayRunning}
            />
          )}

          {(() => {
            const visibleMessages = messages.filter((msg) =>
              shouldShowAssistantMessage(assistantDetailVisibility, msg.kind),
            )

            // Map the archived transcript-entry count onto the visible list:
            // archived entries that are hidden by the current detail filter
            // must not shift the divider position.
            const archivedTranscriptCount = archivedMessageCount ?? 0
            let visibleArchivedCount = 0
            for (
              let i = 0;
              i < messages.length && i < archivedTranscriptCount;
              i++
            ) {
              if (
                shouldShowAssistantMessage(
                  assistantDetailVisibility,
                  messages[i].kind,
                )
              ) {
                visibleArchivedCount++
              }
            }
            const hasSummary = Boolean(sessionSummary && sessionSummary.trim())

            return visibleMessages.map((msg, visibleIndex) => {
              const isForking = forkingMessageIndex === visibleIndex
              const isDeleting = deletingMessageIndex === visibleIndex
              const isArchived = visibleIndex < visibleArchivedCount
              const showNoticeBefore =
                visibleIndex === visibleArchivedCount &&
                (visibleArchivedCount > 0 || hasSummary)
              const isConversationBoundary =
                msg.role === "user" ||
                (msg.role === "assistant" &&
                  (!msg.kind || msg.kind === "normal"))
              const isLastVisibleMessage =
                visibleIndex === visibleMessages.length - 1
              const hasPendingMessageMutation =
                forkingMessageIndex !== null || deletingMessageIndex !== null

              return (
                <div key={msg.id}>
                  {showNoticeBefore && (
                    <CompressedHistoryNotice
                      summary={sessionSummary}
                      showDivider={visibleArchivedCount > 0}
                    />
                  )}
                  <div
                    className={`flex w-full ${isArchived ? "opacity-60" : ""}`}
                  >
                    {msg.role === "assistant" ? (
                      <AssistantMessage
                        content={msg.content}
                        attachments={msg.attachments}
                        kind={msg.kind}
                        modelName={msg.modelName}
                        toolCalls={msg.toolCalls}
                        timestamp={msg.timestamp}
                      />
                    ) : (
                      <UserMessage
                        content={msg.content}
                        attachments={msg.attachments}
                        timestamp={msg.timestamp}
                      />
                    )}
                  </div>

                  {isConversationBoundary && (
                    <div className="flex justify-center gap-1 py-0">
                      {!isLastVisibleMessage && (
                        <Tooltip>
                          <TooltipTrigger asChild>
                            <Button
                              variant="ghost"
                              size="sm"
                              className="text-muted-foreground/60 hover:text-foreground h-7 gap-1.5"
                              disabled={isTyping || hasPendingMessageMutation}
                              onClick={() => handleForkChat(visibleIndex)}
                            >
                              <IconGitFork className="size-3.5" />
                              <span className="text-xs">
                                {isForking ? t("chat.forking") : t("chat.fork")}
                              </span>
                            </Button>
                          </TooltipTrigger>
                          <TooltipContent>
                            {t("chat.forkFromHere")}
                          </TooltipContent>
                        </Tooltip>
                      )}

                      <Tooltip>
                        <TooltipTrigger asChild>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="text-muted-foreground/60 hover:bg-destructive/10 hover:text-destructive h-7 gap-1.5"
                            disabled={isTyping || hasPendingMessageMutation}
                            onClick={() =>
                              setPendingMessageDelete({
                                visibleIndex,
                                archived: isArchived,
                              })
                            }
                          >
                            {isDeleting ? (
                              <IconLoader2 className="size-3.5 animate-spin" />
                            ) : (
                              <IconTrash className="size-3.5" />
                            )}
                            <span className="text-xs">
                              {isDeleting
                                ? t("chat.deletingMessage")
                                : t("chat.deleteMessage")}
                            </span>
                          </Button>
                        </TooltipTrigger>
                        <TooltipContent>
                          {t("chat.deleteMessageFromHere")}
                        </TooltipContent>
                      </Tooltip>
                    </div>
                  )}
                </div>
              )
            })
          })()}

          {isTyping && <TypingIndicator />}
        </div>
      </div>

      <input
        ref={fileInputRef}
        type="file"
        accept={CHAT_IMAGE_ACCEPT}
        multiple
        className="hidden"
        onChange={handleImageSelection}
      />

      <ChatComposer
        input={input}
        attachments={attachments}
        onInputChange={setInput}
        onAddImages={handleAddImages}
        onPaste={handleComposerPaste}
        onDragEnter={handleComposerDragEnter}
        onDragLeave={handleComposerDragLeave}
        onDragOver={handleComposerDragOver}
        onDrop={handleComposerDrop}
        onRemoveAttachment={handleRemoveAttachment}
        onSend={handleSend}
        onContextDetail={() => {
          if (sendMessage({ content: "/context", attachments: [] })) {
            setInput("")
          }
        }}
        inputDisabledReason={inputDisabledReason}
        canSend={canSubmit}
        isDragActive={isDragActive}
        contextUsage={contextUsage}
      />

      <AlertDialog
        open={pendingMessageDelete !== null}
        onOpenChange={(open) => {
          if (!open && deletingMessageIndex === null) {
            setPendingMessageDelete(null)
          }
        }}
      >
        <AlertDialogContent size="sm">
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("chat.deleteMessageConfirmTitle")}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {pendingMessageDelete?.archived
                ? t("chat.deleteArchivedMessageConfirm")
                : t("chat.deleteMessageConfirm")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deletingMessageIndex !== null}>
              {t("chat.deleteMessageCancel")}
            </AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={deletingMessageIndex !== null}
              onClick={(event) => {
                event.preventDefault()
                void handleDeleteMessageSeries()
              }}
            >
              {deletingMessageIndex !== null ? (
                <IconLoader2 className="size-4 animate-spin" />
              ) : (
                <IconTrash className="size-4" />
              )}
              {deletingMessageIndex !== null
                ? t("chat.deletingMessage")
                : t("chat.deleteMessageConfirmButton")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
