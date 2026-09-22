import { createFileRoute } from "@tanstack/react-router"

import { ChatPage } from "@/components/chat/chat-page"

export interface ChatSearch {
  category?: string
}

export const Route = createFileRoute("/")({
  validateSearch: (search: Record<string, unknown>): ChatSearch => {
    const category = search.category
    if (typeof category === "string" && category.trim() !== "") {
      return { category: category.trim() }
    }
    return {}
  },
  component: ChatPage,
})
