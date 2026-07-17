import {
  IconDatabase,
  IconLoader2,
  IconPhoto,
  IconPlus,
  IconStar,
} from "@tabler/icons-react"
import { useCallback, useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  type ModelInfo,
  type ModelProviderOption,
  getModels,
  setDefaultModel,
  setVisionFallbackModel,
} from "@/api/models"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { showSaveSuccessOrRestartToast } from "@/lib/restart-required"
import { refreshGatewayState } from "@/store/gateway"

import { AddModelSheet } from "./add-model-sheet"
import { CatalogDialog } from "./catalog-dialog"
import { DeleteModelDialog } from "./delete-model-dialog"
import { EditModelSheet } from "./edit-model-sheet"
import {
  getCanonicalProviderKey,
  getProviderCatalogMap,
} from "./provider-registry"
import type { ProviderCatalogEntry } from "./provider-registry"
import { ProviderSection } from "./provider-section"

interface ProviderGroup {
  key: string
  provider: Pick<ProviderCatalogEntry, "key" | "label" | "iconSlug" | "domain">
  models: ModelInfo[]
  hasDefault: boolean
  availableCount: number
}

export function ModelsPage() {
  const { t } = useTranslation()
  const [models, setModels] = useState<ModelInfo[]>([])
  const [providerOptions, setProviderOptions] = useState<ModelProviderOption[]>(
    [],
  )
  const [loading, setLoading] = useState(true)
  const [fetchError, setFetchError] = useState("")
  const [visionFallbackModel, setVisionFallbackModelName] = useState("")
  const [settingVisionFallback, setSettingVisionFallback] = useState(false)

  const [editingModel, setEditingModel] = useState<ModelInfo | null>(null)
  const [deletingModel, setDeletingModel] = useState<ModelInfo | null>(null)
  const [addOpen, setAddOpen] = useState(false)
  const [catalogOpen, setCatalogOpen] = useState(false)
  const [settingDefaultIndex, setSettingDefaultIndex] = useState<number | null>(
    null,
  )
  const providerMap = getProviderCatalogMap(providerOptions)

  const fetchModels = useCallback(async () => {
    setLoading(true)
    try {
      const data = await getModels()
      const sorted = [...data.models].sort((a, b) => {
        if (a.is_default && !b.is_default) return -1
        if (!a.is_default && b.is_default) return 1
        if (a.available && !b.available) return -1
        if (!a.available && b.available) return 1
        return a.model_name.localeCompare(b.model_name)
      })
      setModels(sorted)
      setVisionFallbackModelName(data.vision_fallback_model || "")
      setProviderOptions(data.provider_options || [])
      setFetchError("")
    } catch (e) {
      setFetchError(e instanceof Error ? e.message : t("models.loadError"))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchModels()
  }, [fetchModels])

  const handleSetDefault = async (model: ModelInfo) => {
    if (model.is_default) return

    setSettingDefaultIndex(model.index)
    try {
      await setDefaultModel(model.model_name)
      await fetchModels()
      const gateway = await refreshGatewayState({ force: true })
      showSaveSuccessOrRestartToast(
        t,
        t("models.defaultChangeSuccess"),
        model.model_name,
        gateway?.restartRequired === true,
      )
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t("models.loadError"))
    } finally {
      setSettingDefaultIndex(null)
    }
  }

  const handleSetVisionFallback = async (value: string) => {
    const modelName = value === "__none__" ? "" : value
    if (modelName === visionFallbackModel) return

    setSettingVisionFallback(true)
    try {
      await setVisionFallbackModel(modelName)
      await fetchModels()
      const gateway = await refreshGatewayState({ force: true })
      showSaveSuccessOrRestartToast(
        t,
        t("models.visionFallback.saveSuccess"),
        modelName || t("models.visionFallback.none"),
        gateway?.restartRequired === true,
      )
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t("models.loadError"))
    } finally {
      setSettingVisionFallback(false)
    }
  }

  const grouped: Record<
    string,
    {
      provider: Pick<
        ProviderCatalogEntry,
        "key" | "label" | "iconSlug" | "domain"
      >
      models: ModelInfo[]
    }
  > = {}
  for (const model of models) {
    const providerKey = getCanonicalProviderKey(model.provider, providerOptions)
    const providerDef = providerKey ? providerMap.get(providerKey) : undefined
    if (!grouped[providerKey]) {
      grouped[providerKey] = {
        provider: {
          key: providerKey,
          label: providerDef?.label || providerKey,
          iconSlug: providerDef?.iconSlug,
          domain: providerDef?.domain,
        },
        models: [],
      }
    }
    grouped[providerKey].models.push(model)
  }

  const providerGroups: ProviderGroup[] = Object.entries(grouped)
    .map(([key, group]) => {
      const availableCount = group.models.filter(
        (model) => model.available,
      ).length
      return {
        key,
        provider: group.provider,
        models: group.models,
        hasDefault: group.models.some((model) => model.is_default),
        availableCount,
      }
    })
    .sort((a, b) => {
      if (a.hasDefault && !b.hasDefault) return -1
      if (!a.hasDefault && b.hasDefault) return 1

      if (a.availableCount !== b.availableCount) {
        return b.availableCount - a.availableCount
      }

      const aPriority = -(providerMap.get(a.key)?.priority ?? 0)
      const bPriority = -(providerMap.get(b.key)?.priority ?? 0)
      if (aPriority !== bPriority) {
        return aPriority - bPriority
      }

      return a.provider.label.localeCompare(b.provider.label)
    })

  const defaultModel = models.find((model) => model.is_default)
  const visionFallbackOptions = models.filter((model, index, all) => {
    const hasVisionTag = model.tags?.some(
      (tag) => tag.trim().toLowerCase() === "vision",
    )
    const isConfiguredFallback = model.model_name === visionFallbackModel
    const firstWithName = all.findIndex(
      (candidate) => candidate.model_name === model.model_name,
    )
    return (
      (hasVisionTag || isConfiguredFallback) &&
      !model.is_virtual &&
      model.default_model_allowed !== false &&
      (model.available || isConfiguredFallback) &&
      firstWithName === index
    )
  })

  return (
    <div className="flex h-full flex-col">
      <PageHeader title={t("navigation.models")}>
        <div className="flex items-center gap-3">
          <Button
            size="sm"
            variant="outline"
            onClick={() => setCatalogOpen(true)}
            disabled={providerOptions.length === 0}
          >
            <IconDatabase className="size-4" />
            {t("models.catalog.button")}
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() => setAddOpen(true)}
            disabled={providerOptions.length === 0}
          >
            <IconPlus className="size-4" />
            {t("models.add.button")}
          </Button>
        </div>
      </PageHeader>

      <div className="min-h-0 flex-1 overflow-y-auto px-4 sm:px-6">
        <div className="pt-2">
          {!defaultModel && (
            <div className="text-muted-foreground flex items-center gap-1.5 text-sm">
              <span>{t("models.noDefaultHintPrefix")}</span>
              <IconStar className="size-3.5 shrink-0" />
              <span>{t("models.noDefaultHintSuffix")}</span>
            </div>
          )}
          <p className="text-muted-foreground mt-1 text-sm">
            {t("models.description")}
          </p>
          <div className="border-border/60 bg-card mt-4 flex flex-col gap-3 rounded-lg border p-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex min-w-0 items-start gap-2.5">
              <IconPhoto className="text-muted-foreground mt-0.5 size-4 shrink-0" />
              <div>
                <p className="text-sm font-medium">
                  {t("models.visionFallback.label")}
                </p>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  {t("models.visionFallback.description")}
                </p>
              </div>
            </div>
            <Select
              value={visionFallbackModel || "__none__"}
              onValueChange={(value) => void handleSetVisionFallback(value)}
              disabled={loading || settingVisionFallback}
            >
              <SelectTrigger className="w-full sm:w-64">
                {settingVisionFallback && (
                  <IconLoader2 className="size-3.5 animate-spin" />
                )}
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="__none__">
                  {t("models.visionFallback.none")}
                </SelectItem>
                {visionFallbackOptions.map((model) => (
                  <SelectItem key={model.model_name} value={model.model_name}>
                    {model.model_name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {!loading && visionFallbackOptions.length === 0 && (
            <p className="text-muted-foreground mt-1 text-xs">
              {t("models.visionFallback.noOptions")}
            </p>
          )}
          {!loading && providerOptions.length === 0 && (
            <p className="text-muted-foreground mt-1 text-sm">
              {t("models.providerCatalogUnavailable")}
            </p>
          )}
        </div>

        {loading && (
          <div className="flex items-center justify-center py-20">
            <IconLoader2 className="text-muted-foreground size-6 animate-spin" />
          </div>
        )}

        {fetchError && (
          <div className="bg-destructive/10 rounded-lg px-4 py-3 text-sm">
            <p className="text-destructive">{fetchError}</p>
            <div className="mt-3 flex items-center gap-2">
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  void fetchModels()
                }}
              >
                {t("models.retry")}
              </Button>
            </div>
          </div>
        )}

        {!loading && !fetchError && (
          <div className="pb-8">
            {providerGroups.map((providerGroup) => (
              <ProviderSection
                key={providerGroup.key}
                provider={providerGroup.provider}
                models={providerGroup.models}
                onEdit={setEditingModel}
                onSetDefault={handleSetDefault}
                onDelete={setDeletingModel}
                settingDefaultIndex={settingDefaultIndex}
              />
            ))}
          </div>
        )}
      </div>

      <EditModelSheet
        model={editingModel}
        open={editingModel !== null}
        onClose={() => setEditingModel(null)}
        onSaved={fetchModels}
        providerOptions={providerOptions}
      />

      <AddModelSheet
        open={addOpen}
        onClose={() => setAddOpen(false)}
        onSaved={fetchModels}
        existingModelNames={models.map((model) => model.model_name)}
        providerOptions={providerOptions}
      />

      <DeleteModelDialog
        model={deletingModel}
        onClose={() => setDeletingModel(null)}
        onDeleted={fetchModels}
      />

      <CatalogDialog
        open={catalogOpen}
        onClose={() => setCatalogOpen(false)}
        onModelAdded={fetchModels}
        providerOptions={providerOptions}
      />
    </div>
  )
}
