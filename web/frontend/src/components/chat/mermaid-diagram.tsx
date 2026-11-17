import { useEffect, useState } from "react"

import { cn } from "@/lib/utils"

import { MessageCodeBlock } from "./message-code-block"

type MermaidApi = typeof import("mermaid").default

const RENDER_DEBOUNCE_MS = 200

let mermaidApiPromise: Promise<MermaidApi> | null = null
let renderSequence = 0

function isDarkTheme() {
  return document.documentElement.classList.contains("dark")
}

async function loadMermaidApi(): Promise<MermaidApi> {
  if (!mermaidApiPromise) {
    mermaidApiPromise = import("mermaid").then((mermaid) => {
      mermaid.default.initialize({
        startOnLoad: false,
        securityLevel: "strict",
        suppressErrorRendering: true,
      })
      return mermaid.default
    })
  }
  return mermaidApiPromise
}

function removeRenderArtifacts(renderId: string) {
  document.getElementById(renderId)?.remove()
  document.getElementById(`d${renderId}`)?.remove()
}

interface MermaidDiagramProps {
  code: string
  className?: string
}

export function MermaidDiagram({ code, className }: MermaidDiagramProps) {
  const [svg, setSvg] = useState<string | null>(null)
  const [failed, setFailed] = useState(false)
  const [themeVersion, setThemeVersion] = useState(0)

  useEffect(() => {
    const root = document.documentElement
    const observer = new MutationObserver(() => {
      setThemeVersion((version) => version + 1)
    })
    observer.observe(root, {
      attributes: true,
      attributeFilter: ["class"],
    })
    return () => observer.disconnect()
  }, [])

  useEffect(() => {
    let cancelled = false
    const timer = window.setTimeout(() => {
      const renderId = `picoclaw-mermaid-${++renderSequence}`
      loadMermaidApi()
        .then(async (mermaid) => {
          mermaid.initialize({
            theme: isDarkTheme() ? "dark" : "default",
          })
          const rendered = await mermaid.render(renderId, code)
          if (!cancelled) {
            setSvg(rendered.svg)
            setFailed(false)
          }
        })
        .catch(() => {
          if (!cancelled) {
            setSvg(null)
            setFailed(true)
          }
        })
        .finally(() => {
          removeRenderArtifacts(renderId)
        })
    }, RENDER_DEBOUNCE_MS)
    return () => {
      cancelled = true
      window.clearTimeout(timer)
    }
  }, [code, themeVersion])

  if (failed) {
    return (
      <MessageCodeBlock
        code={code}
        language="mermaid"
        className={className}
        trimTrailingEmptyLine
      />
    )
  }

  return (
    <div
      data-picoclaw-mermaid=""
      className={cn(
        "not-prose my-4 overflow-hidden rounded-lg border border-[#d0d7de] bg-[#f6f8fa] text-[#24292f] shadow-xs dark:border-[#30363d] dark:bg-[#0d1117] dark:text-[#c9d1d9]",
        className,
      )}
    >
      <div className="flex items-center justify-between gap-2 border-b border-[#d0d7de] bg-black/[0.03] px-3 py-2 dark:border-[#30363d] dark:bg-white/[0.03]">
        <span className="text-[11px] font-medium text-zinc-600 dark:text-zinc-400">
          mermaid
        </span>
      </div>
      <div className="flex justify-center overflow-x-auto px-4 py-4">
        {svg === null ? (
          <div className="h-16 w-full max-w-xs animate-pulse rounded-md bg-zinc-300/50 dark:bg-zinc-700/50" />
        ) : (
          <div
            className="[&>svg]:h-auto [&>svg]:max-w-full"
            dangerouslySetInnerHTML={{ __html: svg }}
          />
        )}
      </div>
    </div>
  )
}
