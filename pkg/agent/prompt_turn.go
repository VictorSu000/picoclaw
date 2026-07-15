package agent

import (
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func promptBuildRequestForTurn(
	ts *turnState,
	history []providers.Message,
	summary string,
	currentMessage string,
	media []string,
	cfg *config.Config,
) PromptBuildRequest {
	req := PromptBuildRequest{
		History:           history,
		Summary:           summary,
		CurrentMessage:    currentMessage,
		Media:             append([]string(nil), media...),
		Channel:           ts.channel,
		ChatID:            ts.chatID,
		SenderID:          ts.opts.Dispatch.SenderID(),
		SenderDisplayName: ts.opts.SenderDisplayName,
		ActiveSkills:      activeSkillNames(ts.agent, ts.opts),
		Overlays:          promptOverlaysForOptions(ts.opts),
	}
	hasCallableTools := len(filterToolsForTurn(
		ts.agent,
		ts.agent.Tools.ToProviderDefs(),
		ts.profile,
		ts.preset,
	)) > 0 || turnNativeSearchCallable(cfg, ts.profile, ts.preset, ts.agent)
	if ts.profile.Enabled || ts.preset.Enabled() {
		if !hasCallableTools {
			req.SuppressToolUseRule = true
		}
	}
	if turnProfileSystemPromptOff(ts.profile) {
		req.SuppressDefaultSystemPrompt = true
		req.SuppressSkillContext = true
		req.ToolUseFallback = hasCallableTools
	}
	if allowedSkills, restricted := allowedSkillsForTurn(ts.profile, ts.preset); restricted {
		if len(allowedSkills) == 0 {
			req.SuppressSkillContext = true
		} else {
			req.AllowedSkills = allowedSkills
		}
	}
	if allowedTools, restricted := allowedToolNamesForTurn(ts.agent, ts.profile, ts.preset); restricted {
		if len(allowedTools) == 0 {
			req.SuppressToolUseRule = true
		} else {
			req.AllowedTools = allowedTools
		}
	}
	req.RestrictMCPServers = ts.preset.Enabled() && ts.preset.MCPSpecified
	req.AllowedMCPServers = append([]string(nil), ts.preset.MCP...)
	return req
}

func turnNativeSearchCallable(
	cfg *config.Config,
	turnPolicy config.EffectiveTurnProfile,
	preset config.EffectiveAgentPreset,
	agent *AgentInstance,
) bool {
	if cfg == nil || agent == nil {
		return false
	}
	if !cfg.Tools.IsToolEnabled("web") || !cfg.Tools.Web.PreferNative {
		return false
	}
	if !toolAllowedForTurn(agent, turnPolicy, preset, "web_search") {
		return false
	}
	nativeProvider := agent.Provider
	if preset.Enabled() && preset.Model != nil {
		if candidates := agent.PresetCandidates[strings.ToLower(preset.Name)]; len(candidates) > 0 {
			if provider, err := providerForFallbackCandidate(
				agent,
				agent.Provider,
				candidates,
				candidates[0].Provider,
				candidates[0].Model,
			); err == nil && provider != nil {
				nativeProvider = provider
			}
		}
	}
	nativeSearchProvider, ok := nativeProvider.(providers.NativeSearchCapable)
	return ok && nativeSearchProvider.SupportsNativeSearch()
}

func promptBuildRequestForProcessOptions(
	agent *AgentInstance,
	opts processOptions,
	history []providers.Message,
	summary string,
	currentMessage string,
	media []string,
) PromptBuildRequest {
	req := PromptBuildRequest{
		History:           history,
		Summary:           summary,
		CurrentMessage:    currentMessage,
		Media:             append([]string(nil), media...),
		Channel:           opts.Channel,
		ChatID:            opts.ChatID,
		SenderID:          opts.SenderID,
		SenderDisplayName: opts.SenderDisplayName,
		ActiveSkills:      activeSkillNames(agent, opts),
		Overlays:          promptOverlaysForOptions(opts),
	}
	turnPolicy := opts.TurnProfile
	preset := opts.AgentPreset
	hasCallableTools := true
	if agent != nil {
		hasCallableTools = len(filterToolsForTurn(agent, agent.Tools.ToProviderDefs(), turnPolicy, preset)) > 0
	}
	if turnProfileSystemPromptOff(turnPolicy) {
		req.SuppressDefaultSystemPrompt = true
		req.SuppressSkillContext = true
		req.ToolUseFallback = hasCallableTools
	}
	if (turnPolicy.Enabled || preset.Enabled()) && !hasCallableTools {
		req.SuppressToolUseRule = true
	}
	if allowedSkills, restricted := allowedSkillsForTurn(turnPolicy, preset); restricted {
		if len(allowedSkills) == 0 {
			req.SuppressSkillContext = true
		} else {
			req.AllowedSkills = allowedSkills
		}
	}
	if allowedTools, restricted := allowedToolNamesForTurn(agent, turnPolicy, preset); restricted {
		if len(allowedTools) == 0 {
			req.SuppressToolUseRule = true
		} else {
			req.AllowedTools = allowedTools
		}
	}
	req.RestrictMCPServers = preset.Enabled() && preset.MCPSpecified
	req.AllowedMCPServers = append([]string(nil), preset.MCP...)
	return req
}

func promptOverlaysForOptions(opts processOptions) []PromptPart {
	systemPrompt := strings.TrimSpace(opts.SystemPromptOverride)
	if systemPrompt == "" {
		return nil
	}

	return []PromptPart{
		{
			ID:      "instruction.subturn_profile",
			Layer:   PromptLayerInstruction,
			Slot:    PromptSlotWorkspace,
			Source:  PromptSource{ID: PromptSourceSubTurnProfile, Name: "subturn.profile"},
			Title:   "SubTurn System Instructions",
			Content: systemPrompt,
			Stable:  false,
			Cache:   PromptCacheNone,
		},
	}
}

func promptContentBlock(part PromptPart, cache *providers.CacheControl) providers.ContentBlock {
	if cache == nil {
		cache = cacheControlForPromptPart(part)
	}
	return providers.ContentBlock{
		Type:         "text",
		Text:         part.Content,
		CacheControl: cache,
		PromptLayer:  string(part.Layer),
		PromptSlot:   string(part.Slot),
		PromptSource: string(part.Source.ID),
	}
}

func cacheControlForPromptPart(part PromptPart) *providers.CacheControl {
	switch part.Cache {
	case PromptCacheEphemeral:
		return &providers.CacheControl{Type: "ephemeral"}
	default:
		return nil
	}
}

func promptMessageWithMetadata(
	msg providers.Message,
	layer PromptLayer,
	slot PromptSlot,
	source PromptSourceID,
) providers.Message {
	msg.PromptLayer = string(layer)
	msg.PromptSlot = string(slot)
	msg.PromptSource = string(source)
	return msg
}

func promptMessageWithDefaultMetadata(
	msg providers.Message,
	layer PromptLayer,
	slot PromptSlot,
	source PromptSourceID,
) providers.Message {
	if strings.TrimSpace(msg.PromptSource) != "" {
		return msg
	}
	return promptMessageWithMetadata(msg, layer, slot, source)
}

func userPromptMessage(content string, media []string) providers.Message {
	msg := providers.Message{
		Role:    "user",
		Content: content,
	}
	if len(media) > 0 {
		msg.Media = append([]string(nil), media...)
	}
	return promptMessageWithMetadata(msg, PromptLayerTurn, PromptSlotMessage, PromptSourceUserMessage)
}

func toolResultPromptMessage(content, toolCallID string, media []string) providers.Message {
	msg := providers.Message{
		Role:       "tool",
		Content:    content,
		ToolCallID: toolCallID,
	}
	if len(media) > 0 {
		msg.Media = append([]string(nil), media...)
	}
	return promptMessageWithMetadata(msg, PromptLayerTurn, PromptSlotToolResult, PromptSourceToolResult)
}

func toolImageFollowUpPromptMessage(media []string) providers.Message {
	msg := providers.Message{
		Role:    "user",
		Content: "[Loaded image from tool result above]",
	}
	if len(media) > 0 {
		msg.Media = append([]string(nil), media...)
	}
	return promptMessageWithMetadata(msg, PromptLayerTurn, PromptSlotToolResult, PromptSourceToolResult)
}

func steeringPromptMessage(msg providers.Message) providers.Message {
	return promptMessageWithDefaultMetadata(msg, PromptLayerTurn, PromptSlotSteering, PromptSourceSteering)
}

func subTurnResultPromptMessage(content string) providers.Message {
	return promptMessageWithMetadata(
		providers.Message{Role: "user", Content: fmt.Sprintf("[SubTurn Result] %s", content)},
		PromptLayerTurn,
		PromptSlotSubTurn,
		PromptSourceSubTurnResult,
	)
}

func interruptPromptMessage(content string) providers.Message {
	return promptMessageWithMetadata(
		providers.Message{Role: "user", Content: content},
		PromptLayerTurn,
		PromptSlotInterrupt,
		PromptSourceInterrupt,
	)
}
