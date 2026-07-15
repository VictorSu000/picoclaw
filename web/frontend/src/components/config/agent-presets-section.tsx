import { IconPlus, IconTrash } from "@tabler/icons-react"
import { useQuery } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"

import { getModels } from "@/api/models"
import { getSkills } from "@/api/skills"
import { getTools } from "@/api/tools"
import { ConfigSectionCard } from "@/components/config/config-sections"
import {
  type AgentPresetForm,
  type CoreConfigForm,
  type PresetOverrideMode,
  createEmptyAgentPresetForm,
} from "@/components/config/form-model"
import {
  type MultiSelectOption,
  SearchableMultiSelect,
} from "@/components/config/searchable-multi-select"
import { Field } from "@/components/shared-form"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

interface AgentPresetsSectionProps {
  form: CoreConfigForm
  onChange: (presets: AgentPresetForm[]) => void
  disabled?: boolean
}

function OverrideModeSelect({
  value,
  onChange,
  disabled,
}: {
  value: PresetOverrideMode
  onChange: (value: PresetOverrideMode) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  return (
    <Select
      value={value}
      disabled={disabled}
      onValueChange={(next) => onChange(next as PresetOverrideMode)}
    >
      <SelectTrigger className="h-9 w-full sm:w-40">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="inherit">
          {t("pages.config.agent_preset_inherit")}
        </SelectItem>
        <SelectItem value="custom">
          {t("pages.config.agent_preset_custom")}
        </SelectItem>
      </SelectContent>
    </Select>
  )
}

function ListOverrideField({
  label,
  hint,
  mode,
  value,
  options,
  placeholder,
  onModeChange,
  onValueChange,
  disabled,
}: {
  label: string
  hint: string
  mode: PresetOverrideMode
  value: string[]
  options: MultiSelectOption[]
  placeholder: string
  onModeChange: (mode: PresetOverrideMode) => void
  onValueChange: (value: string[]) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  return (
    <Field label={label} hint={hint} layout="setting-row">
      <div className="space-y-2">
        <OverrideModeSelect
          value={mode}
          onChange={onModeChange}
          disabled={disabled}
        />
        {mode === "custom" && (
          <>
            <SearchableMultiSelect
              value={value}
              options={options}
              onChange={onValueChange}
              placeholder={placeholder}
              disabled={disabled}
            />
            {value.length === 0 && (
              <p className="text-muted-foreground text-xs">
                {t("pages.config.agent_preset_empty_disables")}
              </p>
            )}
          </>
        )}
      </div>
    </Field>
  )
}

export function AgentPresetsSection({
  form,
  onChange,
  disabled,
}: AgentPresetsSectionProps) {
  const { t } = useTranslation()
  const { data: modelsData } = useQuery({
    queryKey: ["models"],
    queryFn: getModels,
  })
  const { data: toolsData } = useQuery({
    queryKey: ["tools"],
    queryFn: getTools,
  })
  const { data: skillsData } = useQuery({
    queryKey: ["skills"],
    queryFn: getSkills,
  })

  const modelNames = Array.from(
    new Set(
      (modelsData?.models ?? [])
        .filter((model) => !model.is_virtual)
        .map((model) => model.model_name),
    ),
  ).sort((left, right) => left.localeCompare(right))
  const toolOptions: MultiSelectOption[] = (toolsData?.tools ?? []).map(
    (tool) => ({
      value: tool.name,
      disabled: tool.status !== "enabled",
      description:
        tool.status === "enabled"
          ? tool.description
          : `${tool.description} (${tool.status})`,
    }),
  )
  const skillOptions: MultiSelectOption[] = (skillsData?.skills ?? []).map(
    (skill) => ({
      value: skill.name,
      description: skill.description,
    }),
  )
  const mcpOptions: MultiSelectOption[] = form.mcpServers
    .filter((server) => server.name.trim())
    .map((server) => ({
      value: server.name.trim(),
      disabled: !form.mcpEnabled || !server.enabled,
      description:
        form.mcpEnabled && server.enabled
          ? undefined
          : t("pages.config.agent_preset_mcp_disabled"),
    }))

  const updatePreset = <K extends keyof AgentPresetForm>(
    id: string,
    key: K,
    value: AgentPresetForm[K],
  ) => {
    onChange(
      form.agentPresets.map((preset) =>
        preset.id === id ? { ...preset, [key]: value } : preset,
      ),
    )
  }

  return (
    <ConfigSectionCard
      title={t("pages.config.sections.agent_presets")}
      description={t("pages.config.agent_presets_hint")}
    >
      <div className="space-y-3 py-4">
        {form.agentPresets.length === 0 && (
          <p className="text-muted-foreground text-sm">
            {t("pages.config.agent_presets_empty")}
          </p>
        )}

        {form.agentPresets.map((preset) => {
          const fallbackOptions = modelNames
            .filter((name) => name !== preset.primaryModel)
            .map((value) => ({ value }))
          const primaryModels = preset.primaryModel
            ? Array.from(new Set([...modelNames, preset.primaryModel]))
            : modelNames

          return (
            <Card key={preset.id} size="sm">
              <CardHeader className="flex flex-row items-center justify-between gap-3 border-b">
                <CardTitle className="min-w-0 flex-1">
                  {preset.name.trim() || t("pages.config.agent_preset_new")}
                </CardTitle>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  disabled={disabled}
                  aria-label={t("pages.config.agent_preset_remove")}
                  onClick={() =>
                    onChange(
                      form.agentPresets.filter((item) => item.id !== preset.id),
                    )
                  }
                >
                  <IconTrash className="text-destructive size-4" />
                </Button>
              </CardHeader>
              <CardContent className="divide-border/70 divide-y pt-0">
                <Field
                  label={t("pages.config.agent_preset_name")}
                  hint={t("pages.config.agent_preset_name_hint")}
                  layout="setting-row"
                >
                  <Input
                    value={preset.name}
                    disabled={disabled}
                    placeholder="coding"
                    onChange={(event) =>
                      updatePreset(preset.id, "name", event.target.value)
                    }
                  />
                </Field>

                <Field
                  label={t("pages.config.agent_preset_model")}
                  hint={t("pages.config.agent_preset_model_hint")}
                  layout="setting-row"
                >
                  <div className="space-y-2">
                    <OverrideModeSelect
                      value={preset.modelMode}
                      disabled={disabled}
                      onChange={(value) =>
                        updatePreset(preset.id, "modelMode", value)
                      }
                    />
                    {preset.modelMode === "custom" && (
                      <>
                        <Select
                          value={preset.primaryModel}
                          disabled={disabled}
                          onValueChange={(value) =>
                            updatePreset(preset.id, "primaryModel", value)
                          }
                        >
                          <SelectTrigger>
                            <SelectValue
                              placeholder={t(
                                "pages.config.agent_preset_primary_model",
                              )}
                            />
                          </SelectTrigger>
                          <SelectContent>
                            {primaryModels.map((name) => (
                              <SelectItem key={name} value={name}>
                                {name}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                        <SearchableMultiSelect
                          value={preset.fallbackModels}
                          options={fallbackOptions}
                          disabled={disabled}
                          placeholder={t(
                            "pages.config.agent_preset_fallback_models",
                          )}
                          onChange={(value) =>
                            updatePreset(preset.id, "fallbackModels", value)
                          }
                        />
                      </>
                    )}
                  </div>
                </Field>

                <ListOverrideField
                  label={t("pages.config.agent_preset_tools")}
                  hint={t("pages.config.agent_preset_tools_hint")}
                  mode={preset.toolsMode}
                  value={preset.tools}
                  options={toolOptions}
                  disabled={disabled}
                  placeholder={t("pages.config.agent_preset_select_tools")}
                  onModeChange={(value) =>
                    updatePreset(preset.id, "toolsMode", value)
                  }
                  onValueChange={(value) =>
                    updatePreset(preset.id, "tools", value)
                  }
                />
                <ListOverrideField
                  label={t("pages.config.agent_preset_skills")}
                  hint={t("pages.config.agent_preset_skills_hint")}
                  mode={preset.skillsMode}
                  value={preset.skills}
                  options={skillOptions}
                  disabled={disabled}
                  placeholder={t("pages.config.agent_preset_select_skills")}
                  onModeChange={(value) =>
                    updatePreset(preset.id, "skillsMode", value)
                  }
                  onValueChange={(value) =>
                    updatePreset(preset.id, "skills", value)
                  }
                />
                <ListOverrideField
                  label={t("pages.config.agent_preset_mcp")}
                  hint={t("pages.config.agent_preset_mcp_hint")}
                  mode={preset.mcpMode}
                  value={preset.mcpServers}
                  options={mcpOptions}
                  disabled={disabled}
                  placeholder={t("pages.config.agent_preset_select_mcp")}
                  onModeChange={(value) =>
                    updatePreset(preset.id, "mcpMode", value)
                  }
                  onValueChange={(value) =>
                    updatePreset(preset.id, "mcpServers", value)
                  }
                />
              </CardContent>
            </Card>
          )
        })}

        <Button
          type="button"
          variant="outline"
          disabled={disabled}
          onClick={() =>
            onChange([
              ...form.agentPresets,
              createEmptyAgentPresetForm(form.agentPresets.length + 1),
            ])
          }
        >
          <IconPlus className="size-4" />
          {t("pages.config.agent_preset_add")}
        </Button>
      </div>
    </ConfigSectionCard>
  )
}
