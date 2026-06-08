import { createFileRoute } from "@tanstack/react-router"
import React from "react"

export const Route = createFileRoute("/app/$id")({
  component: ExternalAppPage,
})

function ExternalAppPage() {
  const { id } = Route.useParams()
  const [loading, setLoading] = React.useState(true)
  const [error, setError] = React.useState<string | null>(null)

  const iframeUrl = `/_external-app/${id}/`

  const handleIframeLoad = () => {
    setLoading(false)
  }

  const handleIframeError = () => {
    setError("Failed to load external application")
    setLoading(false)
  }

  return (
    <div className="h-full w-full flex flex-col">
      {/* Loading indicator */}
      {loading && (
        <div className="flex items-center justify-center h-full bg-background">
          <div className="text-center">
            <div className="inline-block animate-spin rounded-full h-8 w-8 border-t-2 border-b-2 border-primary mb-4"></div>
            <p className="text-muted-foreground">Loading application...</p>
          </div>
        </div>
      )}

      {/* Error message */}
      {error && (
        <div className="flex items-center justify-center h-full bg-background">
          <div className="text-center">
            <p className="text-destructive font-semibold mb-2">{error}</p>
            <p className="text-sm text-muted-foreground">
              Application ID: <code className="bg-muted px-2 py-1 rounded">{id}</code>
            </p>
          </div>
        </div>
      )}

      {/* Iframe container */}
      <iframe
        key={id}
        src={iframeUrl}
        title={`External App: ${id}`}
        className="flex-1 w-full border-0 bg-background"
        onLoad={handleIframeLoad}
        onError={handleIframeError}
        sandbox="allow-same-origin allow-scripts allow-forms allow-popups allow-modals allow-presentation"
      />
    </div>
  )
}
