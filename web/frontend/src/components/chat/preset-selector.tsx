import { useTranslation } from "react-i18next"

import type { AgentPresetListItem } from "@/api/agent-presets"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

interface PresetSelectorProps {
  value: string
  presets: AgentPresetListItem[]
  disabled?: boolean
  onValueChange: (value: string) => void
}

export function PresetSelector({
  value,
  presets,
  disabled,
  onValueChange,
}: PresetSelectorProps) {
  const { t } = useTranslation()
  const configuredPreset = presets.find(
    (preset) => preset.name.toLowerCase() === value.toLowerCase(),
  )
  const selectedValue =
    value.toLowerCase() === "default"
      ? "default"
      : (configuredPreset?.name ?? value)
  const knownValue = selectedValue === "default" || Boolean(configuredPreset)

  return (
    <Select
      value={selectedValue}
      disabled={disabled}
      onValueChange={onValueChange}
    >
      <SelectTrigger
        size="sm"
        aria-label={t("chat.selectPreset")}
        className="text-muted-foreground hover:text-foreground focus-visible:border-input h-8 max-w-[150px] min-w-[92px] bg-transparent shadow-none focus-visible:ring-0 sm:max-w-[200px]"
      >
        <SelectValue placeholder={t("chat.selectPreset")} />
      </SelectTrigger>
      <SelectContent position="popper" align="start">
        <SelectItem value="default">{t("chat.defaultPreset")}</SelectItem>
        {!knownValue && selectedValue !== "default" && (
          <SelectItem value={selectedValue} disabled>
            {selectedValue} ({t("chat.missingPreset")})
          </SelectItem>
        )}
        {presets.map((preset) => (
          <SelectItem key={preset.name} value={preset.name}>
            <span className="flex min-w-0 flex-col">
              <span>{preset.name}</span>
              <span className="text-muted-foreground truncate text-xs">
                {preset.effective_model}
              </span>
            </span>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
