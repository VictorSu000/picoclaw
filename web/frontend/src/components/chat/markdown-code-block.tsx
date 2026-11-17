import type { ComponentProps } from "react"

import { MermaidDiagram } from "./mermaid-diagram"
import { MessageCodeBlock } from "./message-code-block"
import {
  type MarkdownNode,
  extractCodeBlockFromPreNode,
} from "./message-code-block.utils"

export interface MarkdownCodeBlockProps extends ComponentProps<"pre"> {
  node?: MarkdownNode
}

export function MarkdownCodeBlock({
  children,
  className,
  node,
}: MarkdownCodeBlockProps) {
  const { code, language } = extractCodeBlockFromPreNode(node)

  if (language === "mermaid") {
    return <MermaidDiagram code={code} className={className} />
  }

  return (
    <MessageCodeBlock
      code={code}
      language={language}
      bodyClassName={className}
      trimTrailingEmptyLine
    >
      {children}
    </MessageCodeBlock>
  )
}
