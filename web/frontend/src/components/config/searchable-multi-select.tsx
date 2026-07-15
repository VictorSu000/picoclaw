import { IconCheck, IconChevronDown, IconX } from "@tabler/icons-react"
import { useState } from "react"
import { useTranslation } from "react-i18next"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import { cn } from "@/lib/utils"

export interface MultiSelectOption {
  value: string
  label?: string
  disabled?: boolean
  description?: string
}

interface SearchableMultiSelectProps {
  value: string[]
  options: MultiSelectOption[]
  onChange: (value: string[]) => void
  placeholder: string
  disabled?: boolean
}

export function SearchableMultiSelect({
  value,
  options,
  onChange,
  placeholder,
  disabled,
}: SearchableMultiSelectProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const selected = new Set(value.map((item) => item.toLowerCase()))

  const toggle = (item: string) => {
    const exists = selected.has(item.toLowerCase())
    onChange(
      exists
        ? value.filter((current) => current.toLowerCase() !== item.toLowerCase())
        : [...value, item],
    )
  }

  return (
    <div className="space-y-2">
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button
            type="button"
            variant="outline"
            role="combobox"
            aria-expanded={open}
            disabled={disabled}
            className="w-full justify-between font-normal"
          >
            <span className={cn(!value.length && "text-muted-foreground")}>
              {value.length
                ? t("pages.config.agent_preset_selected_count", {
                    count: value.length,
                  })
                : placeholder}
            </span>
            <IconChevronDown className="size-4 opacity-50" />
          </Button>
        </PopoverTrigger>
        <PopoverContent
          align="start"
          className="w-[--radix-popover-trigger-width] p-0"
        >
          <Command>
            <CommandInput
              placeholder={t("pages.config.agent_preset_search")}
            />
            <CommandList>
              <CommandEmpty>
                {t("pages.config.agent_preset_no_options")}
              </CommandEmpty>
              <CommandGroup>
                {options.map((option) => {
                  const isSelected = selected.has(option.value.toLowerCase())
                  return (
                    <CommandItem
                      key={option.value}
                      value={option.value}
                      keywords={[option.label ?? option.value]}
                      disabled={option.disabled && !isSelected}
                      onSelect={() => toggle(option.value)}
                    >
                      <IconCheck
                        className={cn(
                          "size-4",
                          isSelected ? "opacity-100" : "opacity-0",
                        )}
                      />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate">
                          {option.label ?? option.value}
                        </span>
                        {option.description && (
                          <span className="text-muted-foreground block truncate text-xs">
                            {option.description}
                          </span>
                        )}
                      </span>
                    </CommandItem>
                  )
                })}
              </CommandGroup>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>

      {value.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {value.map((item) => (
            <Badge key={item} variant="secondary" className="gap-1 font-mono">
              {item}
              <button
                type="button"
                aria-label={t("pages.config.agent_preset_remove_value", {
                  value: item,
                })}
                disabled={disabled}
                onClick={() => toggle(item)}
                className="hover:text-destructive"
              >
                <IconX className="size-3" />
              </button>
            </Badge>
          ))}
        </div>
      )}
    </div>
  )
}
