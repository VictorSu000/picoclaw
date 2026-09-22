import { IconChevronRight } from "@tabler/icons-react"
import {
  IconAtom,
  IconChevronsDown,
  IconChevronsUp,
  IconKey,
  IconListDetails,
  IconMessageCircle,
  IconPlus,
  IconSearch,
  IconSettings,
  IconSparkles,
  IconTools,
  IconApps,
} from "@tabler/icons-react"
import { Link, useNavigate, useRouterState } from "@tanstack/react-router"
import React from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
  useSidebar,
} from "@/components/ui/sidebar"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { useSidebarChannels } from "@/hooks/use-sidebar-channels"
import { getExternalApps, type ExternalApp } from "@/api/external-apps"
import {
  createCategory,
  getCategories,
  DEFAULT_CATEGORY_ID,
  type Category,
} from "@/api/categories"

interface NavItem {
  title: string
  url: string
  icon: React.ComponentType<{ className?: string }>
  translateTitle?: boolean
  /** When set, this item navigates to / with ?category= (chat category tab). */
  categoryId?: string
}

interface NavGroup {
  label: string
  defaultOpen: boolean
  items: NavItem[]
  isChannelsGroup?: boolean
}

const baseNavGroups: Omit<NavGroup, "items">[] = [
  {
    label: "navigation.chat",
    defaultOpen: true,
  },
  {
    label: "navigation.model_group",
    defaultOpen: true,
  },
  {
    label: "navigation.agent_group",
    defaultOpen: false,
  },
  {
    label: "navigation.services",
    defaultOpen: true,
  },
]

export function AppSidebar({ ...props }: React.ComponentProps<typeof Sidebar>) {
  const routerState = useRouterState()
  const navigate = useNavigate()
  const { i18n, t } = useTranslation()
  const { isMobile, setOpenMobile } = useSidebar()
  const currentPath = routerState.location.pathname
  const currentSearch = routerState.location.search as Record<string, unknown>
  const activeCategory =
    typeof currentSearch?.category === "string" && currentSearch.category
      ? currentSearch.category
      : DEFAULT_CATEGORY_ID
  const [externalApps, setExternalApps] = React.useState<ExternalApp[]>([])
  const [categories, setCategories] = React.useState<Category[]>([])
  const [createOpen, setCreateOpen] = React.useState(false)
  const [newCategoryName, setNewCategoryName] = React.useState("")
  const [creating, setCreating] = React.useState(false)
  const {
    channelItems,
    hasMoreChannels,
    showAllChannels,
    toggleShowAllChannels,
  } = useSidebarChannels({
    language: (i18n.resolvedLanguage ?? i18n.language ?? "").toLowerCase(),
    t,
  })

  // Load external apps on mount
  React.useEffect(() => {
    const loadExternalApps = async () => {
      try {
        const apps = await getExternalApps()
        setExternalApps(apps || [])
      } catch (err) {
        console.error("Failed to load external apps:", err)
      }
    }
    loadExternalApps()
  }, [])

  // Load conversation categories
  React.useEffect(() => {
    let cancelled = false
    const loadCategories = async () => {
      try {
        const data = await getCategories()
        if (!cancelled) setCategories(data)
      } catch (err) {
        console.error("Failed to load categories:", err)
      }
    }
    void loadCategories()
    return () => {
      cancelled = true
    }
  }, [])

  const handleNavItemClick = React.useCallback(() => {
    if (isMobile) {
      setOpenMobile(false)
    }
  }, [isMobile, setOpenMobile])

  const handleCreateCategory = async () => {
    const name = newCategoryName.trim()
    if (!name || creating) return
    setCreating(true)
    try {
      const created = await createCategory(name)
      setCategories((prev) => {
        const defaultCat: Category = {
          id: DEFAULT_CATEGORY_ID,
          name: "",
          created: new Date(0).toISOString(),
        }
        const existingDefault =
          prev.find((c) => c.id === DEFAULT_CATEGORY_ID) ?? defaultCat
        const others = prev.filter(
          (c) => c.id !== DEFAULT_CATEGORY_ID && c.id !== created.id,
        )
        return [existingDefault, ...others, created]
      })
      setCreateOpen(false)
      setNewCategoryName("")
      handleNavItemClick()
      await navigate({
        to: "/",
        search: { category: created.id },
      })
    } catch (err) {
      console.error("Failed to create category:", err)
      toast.error(
        err instanceof Error && err.message === "duplicate"
          ? t("categories.duplicateName")
          : t("categories.createFailed"),
      )
    } finally {
      setCreating(false)
    }
  }

  const navGroups: NavGroup[] = React.useMemo(() => {
    const categoryItems: NavItem[] = categories.map((cat) => ({
      title: cat.id === DEFAULT_CATEGORY_ID ? "categories.default" : cat.name,
      url: cat.id === DEFAULT_CATEGORY_ID ? "/" : `/?category=${cat.id}`,
      icon: IconMessageCircle,
      translateTitle: cat.id === DEFAULT_CATEGORY_ID,
      categoryId: cat.id,
    }))
    // Ensure default tab exists even before categories load.
    if (categories.length === 0) {
      categoryItems.push({
        title: "categories.default",
        url: "/",
        icon: IconMessageCircle,
        translateTitle: true,
        categoryId: DEFAULT_CATEGORY_ID,
      })
    }

    const groups: NavGroup[] = [
      {
        ...baseNavGroups[0],
        items: categoryItems,
      },
      {
        ...baseNavGroups[1],
        items: [
          {
            title: "navigation.models",
            url: "/models",
            icon: IconAtom,
            translateTitle: true,
          },
          {
            title: "navigation.credentials",
            url: "/credentials",
            icon: IconKey,
            translateTitle: true,
          },
        ],
      },
      {
        label: "navigation.channels_group",
        defaultOpen: false,
        items: channelItems.map((item) => ({
          title: item.title,
          url: item.url,
          icon: item.icon,
          translateTitle: false,
        })),
        isChannelsGroup: true,
      },
      {
        ...baseNavGroups[2],
        items: [
          {
            title: "navigation.hub",
            url: "/agent/hub",
            icon: IconSearch,
            translateTitle: true,
          },
          {
            title: "navigation.skills",
            url: "/agent/skills",
            icon: IconSparkles,
            translateTitle: true,
          },
          {
            title: "navigation.tools",
            url: "/agent/tools",
            icon: IconTools,
            translateTitle: true,
          },
        ],
      },
      {
        ...baseNavGroups[3],
        items: [
          {
            title: "navigation.config",
            url: "/config",
            icon: IconSettings,
            translateTitle: true,
          },
          {
            title: "navigation.logs",
            url: "/logs",
            icon: IconListDetails,
            translateTitle: true,
          },
        ],
      },
    ]

    // Add external apps group if any apps are configured
    if (externalApps.length > 0) {
      groups.push({
        label: "应用",
        defaultOpen: true,
        items: externalApps.map((app) => ({
          title: app.name,
          url: `/app/${app.id}`,
          icon: IconApps,
          translateTitle: false,
        })),
      })
    }

    return groups
  }, [channelItems, externalApps, categories])

  return (
    <Sidebar
      {...props}
      className="bg-background border-r-border/20 border-r pt-3"
    >
      <SidebarContent className="bg-background">
        {navGroups.map((group, groupIndex) => (
          <Collapsible
            key={group.label}
            defaultOpen={group.defaultOpen}
            className="group/collapsible mb-1"
          >
            <SidebarGroup className="px-2 py-0">
              <SidebarGroupLabel asChild>
                <CollapsibleTrigger className="hover:bg-muted/60 flex w-full cursor-pointer items-center justify-between rounded-md px-2 py-1.5 transition-colors">
                  <span>{t(group.label)}</span>
                  <IconChevronRight className="size-3.5 opacity-50 transition-transform duration-200 group-data-[state=open]/collapsible:rotate-90" />
                </CollapsibleTrigger>
              </SidebarGroupLabel>
              <CollapsibleContent>
                <SidebarGroupContent className="pt-1">
                  <SidebarMenu>
                    {group.items.map((item) => {
                      const isCategoryTab = item.categoryId !== undefined
                      const isActive = isCategoryTab
                        ? currentPath === "/" &&
                          activeCategory === item.categoryId
                        : currentPath === item.url ||
                          (item.url !== "/" &&
                            currentPath.startsWith(`${item.url}/`))
                      return (
                        <SidebarMenuItem key={item.title}>
                          <SidebarMenuButton
                            asChild
                            isActive={isActive}
                            onClick={handleNavItemClick}
                            data-tour={
                              item.url === "/models" ? "models-nav" : undefined
                            }
                            className={`h-9 px-3 ${isActive ? "bg-accent/80 text-foreground font-medium" : "text-muted-foreground hover:bg-muted/60"}`}
                          >
                            {isCategoryTab ? (
                              <Link
                                to="/"
                                search={
                                  item.categoryId &&
                                  item.categoryId !== DEFAULT_CATEGORY_ID
                                    ? { category: item.categoryId }
                                    : {}
                                }
                              >
                                <item.icon
                                  className={`size-4 ${isActive ? "opacity-100" : "opacity-60"}`}
                                />
                                <span
                                  className={
                                    isActive ? "opacity-100" : "opacity-80"
                                  }
                                >
                                  {item.translateTitle === false
                                    ? item.title
                                    : t(item.title)}
                                </span>
                              </Link>
                            ) : (
                              <Link to={item.url}>
                                <item.icon
                                  className={`size-4 ${isActive ? "opacity-100" : "opacity-60"}`}
                                />
                                <span
                                  className={
                                    isActive ? "opacity-100" : "opacity-80"
                                  }
                                >
                                  {item.translateTitle === false
                                    ? item.title
                                    : t(item.title)}
                                </span>
                              </Link>
                            )}
                          </SidebarMenuButton>
                        </SidebarMenuItem>
                      )
                    })}
                    {groupIndex === 0 && (
                      <SidebarMenuItem key="create-category">
                        <SidebarMenuButton
                          onClick={() => setCreateOpen(true)}
                          className="text-muted-foreground hover:bg-muted/60 h-9 px-3"
                        >
                          <IconPlus className="size-4 opacity-60" />
                          <span className="opacity-80">
                            {t("categories.createNew")}
                          </span>
                        </SidebarMenuButton>
                      </SidebarMenuItem>
                    )}
                    {group.isChannelsGroup && hasMoreChannels && (
                      <SidebarMenuItem key="channels-more-toggle">
                        <SidebarMenuButton
                          onClick={toggleShowAllChannels}
                          className="text-muted-foreground hover:bg-muted/60 h-9 px-3"
                        >
                          {showAllChannels ? (
                            <IconChevronsUp className="size-4 opacity-60" />
                          ) : (
                            <IconChevronsDown className="size-4 opacity-60" />
                          )}
                          <span className="opacity-80">
                            {showAllChannels
                              ? t("navigation.show_less_channels")
                              : t("navigation.show_more_channels")}
                          </span>
                        </SidebarMenuButton>
                      </SidebarMenuItem>
                    )}
                  </SidebarMenu>
                </SidebarGroupContent>
              </CollapsibleContent>
            </SidebarGroup>
          </Collapsible>
        ))}
      </SidebarContent>
      <SidebarRail />

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>{t("categories.createNew")}</DialogTitle>
          </DialogHeader>
          <Input
            value={newCategoryName}
            placeholder={t("categories.namePlaceholder")}
            maxLength={40}
            onChange={(event) => setNewCategoryName(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault()
                void handleCreateCategory()
              }
            }}
            autoFocus
          />
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                setCreateOpen(false)
                setNewCategoryName("")
              }}
            >
              {t("categories.cancel")}
            </Button>
            <Button
              onClick={() => void handleCreateCategory()}
              disabled={!newCategoryName.trim() || creating}
            >
              {creating ? t("categories.creating") : t("categories.create")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Sidebar>
  )
}
