//go:build !mipsle && !netbsd && !(freebsd && arm)

package api

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/seahorse"
	"github.com/sipeed/picoclaw/pkg/tokenizer"
)

// reconcileEditedSessionContext invalidates derived Seahorse summaries and
// rebuilds its active message context after a Web UI history edit. The default
// legacy context manager reads JSONL directly and needs no extra reconciliation.
func (h *Handler) reconcileEditedSessionContext(
	ctx context.Context,
	sessionsDir string,
	sessionKey string,
	history []providers.Message,
) error {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(cfg.Agents.Defaults.ContextManager), "seahorse") {
		return nil
	}

	engine, err := seahorse.NewEngine(seahorse.Config{
		DBPath: filepath.Join(sessionsDir, "seahorse.db"),
	}, nil)
	if err != nil {
		return err
	}
	defer engine.Close()

	if err := engine.ClearSession(ctx, sessionKey); err != nil {
		return err
	}
	if len(history) == 0 {
		return nil
	}

	messages := make([]seahorse.Message, 0, len(history))
	for _, msg := range history {
		messages = append(messages, providerMessageToSeahorse(msg))
	}
	return engine.Bootstrap(ctx, sessionKey, messages)
}

func providerMessageToSeahorse(msg providers.Message) seahorse.Message {
	createdAt := time.Time{}
	if msg.CreatedAt != nil && !msg.CreatedAt.IsZero() {
		createdAt = msg.CreatedAt.UTC().Truncate(time.Second)
	}

	result := seahorse.Message{
		Role:             msg.Role,
		Content:          msg.Content,
		ModelName:        msg.ModelName,
		ReasoningContent: msg.ReasoningContent,
		TokenCount:       tokenizer.EstimateMessageTokens(msg),
		CreatedAt:        createdAt,
	}
	for _, toolCall := range msg.ToolCalls {
		name := toolCall.Name
		arguments := ""
		if toolCall.Function != nil {
			if toolCall.Function.Name != "" {
				name = toolCall.Function.Name
			}
			arguments = toolCall.Function.Arguments
		}
		result.Parts = append(result.Parts, seahorse.MessagePart{
			Type:       "tool_use",
			Name:       name,
			Arguments:  arguments,
			ToolCallID: toolCall.ID,
		})
	}
	if msg.ToolCallID != "" {
		result.Parts = append(result.Parts, seahorse.MessagePart{
			Type:       "tool_result",
			Text:       msg.Content,
			ToolCallID: msg.ToolCallID,
		})
	}
	for _, mediaURI := range msg.Media {
		result.Parts = append(result.Parts, seahorse.MessagePart{
			Type:     "media",
			MediaURI: mediaURI,
		})
	}
	return result
}
