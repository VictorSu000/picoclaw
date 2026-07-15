import { useQuery } from "@tanstack/react-query"
import { useCallback, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  getAgentPresets,
  setSessionAgentPreset,
} from "@/api/agent-presets"
import { getChatState, updateChatStore } from "@/store/chat"

interface UseAgentPresetsOptions {
  activeSessionId: string
  agentPresetName: string
  storedEffectiveModelName?: string
}

export function useAgentPresets({
  activeSessionId,
  agentPresetName,
  storedEffectiveModelName,
}: UseAgentPresetsOptions) {
  const { t } = useTranslation()
  const [isChanging, setIsChanging] = useState(false)
  const { data, isLoading } = useQuery({
    queryKey: ["agent-presets", activeSessionId],
    queryFn: () => getAgentPresets(activeSessionId),
  })

  const selectedPreset = useMemo(
    () =>
      data?.presets.find(
        (preset) =>
          preset.name.toLowerCase() === agentPresetName.toLowerCase(),
      ),
    [agentPresetName, data?.presets],
  )
  const isPresetActive = agentPresetName.trim().toLowerCase() !== "default"
  const effectiveModelName = isPresetActive
    ? selectedPreset?.effective_model || storedEffectiveModelName || ""
    : data?.default_model || storedEffectiveModelName || ""
  const fallbacks = isPresetActive
    ? (selectedPreset?.fallbacks ?? [])
    : []

  const changePreset = useCallback(
    async (nextName: string) => {
      const normalized = nextName.trim() || "default"
      if (normalized.toLowerCase() === agentPresetName.toLowerCase()) {
        return
      }

      setIsChanging(true)
      try {
        const result = await setSessionAgentPreset(
          activeSessionId,
          normalized,
        )
        const sessionStillActive =
          getChatState().activeSessionId === activeSessionId
        if (sessionStillActive) {
          updateChatStore({
            agentPresetName: result.agent_preset?.trim() || "default",
            effectiveModelName: result.effective_model?.trim() || undefined,
          })
          toast.success(t("chat.presetChanged", { name: normalized }))
        }
      } catch (error) {
        toast.error(
          error instanceof Error
            ? error.message
            : t("chat.presetChangeFailed"),
        )
      } finally {
        setIsChanging(false)
      }
    },
    [activeSessionId, agentPresetName, t],
  )

  return {
    presets: data?.presets ?? [],
    isLoading,
    isChanging,
    isPresetActive,
    effectiveModelName,
    fallbacks,
    changePreset,
  }
}
