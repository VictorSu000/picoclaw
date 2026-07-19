import { useQuery } from "@tanstack/react-query"
import { useCallback, useState } from "react"
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
  agentPresetOverride: boolean
  storedEffectiveModelName?: string
}

export function useAgentPresets({
  activeSessionId,
  agentPresetName,
  agentPresetOverride,
  storedEffectiveModelName,
}: UseAgentPresetsOptions) {
  const { t } = useTranslation()
  const [isChanging, setIsChanging] = useState(false)
  const { data, isLoading } = useQuery({
    queryKey: ["agent-presets", activeSessionId],
    queryFn: () => getAgentPresets(activeSessionId),
  })

  const inheritedPresetName = data?.default_preset?.trim() || "default"
  const effectivePresetName = agentPresetOverride
    ? agentPresetName
    : inheritedPresetName
  const isPresetActive = effectivePresetName.trim().toLowerCase() !== "default"
  const effectiveSelectedPreset = data?.presets.find(
    (preset) =>
      preset.name.toLowerCase() === effectivePresetName.toLowerCase(),
  )
  const effectiveModelName = isPresetActive
    ? effectiveSelectedPreset?.effective_model || storedEffectiveModelName || ""
    : data?.default_model || storedEffectiveModelName || ""
  const effectiveFallbacks = isPresetActive
    ? (effectiveSelectedPreset?.fallbacks ?? [])
    : []

  const changePreset = useCallback(
    async (nextName: string) => {
      const normalized = nextName.trim() || "default"
      if (normalized.toLowerCase() === effectivePresetName.toLowerCase()) {
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
            agentPresetOverride: result.agent_preset_override === true,
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
    [activeSessionId, effectivePresetName, t],
  )

  return {
    presets: data?.presets ?? [],
    isLoading,
    isChanging,
    isPresetActive,
    effectiveModelName,
    fallbacks: effectiveFallbacks,
    agentPresetName: effectivePresetName,
    changePreset,
  }
}
