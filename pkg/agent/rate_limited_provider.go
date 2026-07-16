package agent

import (
	"context"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// candidateRateLimitedProvider applies the same per-candidate limiter used by
// the normal fallback pipeline to internal LLM calls (for example context
// summarization and evolution). Those calls use an LLMProvider directly and
// would otherwise bypass the model's configured RPM limit.
type candidateRateLimitedProvider struct {
	delegate  providers.LLMProvider
	wait      func(context.Context, providers.FallbackCandidate) error
	candidate providers.FallbackCandidate
}

func withCandidateRateLimit(
	delegate providers.LLMProvider,
	fallback *providers.FallbackChain,
	candidates []providers.FallbackCandidate,
) providers.LLMProvider {
	if delegate == nil || fallback == nil || len(candidates) == 0 {
		return delegate
	}
	return &candidateRateLimitedProvider{
		delegate:  delegate,
		wait:      fallback.WaitForCandidate,
		candidate: candidates[0],
	}
}

// withCurrentCandidateRateLimit is used by long-lived components that survive
// a config reload. It resolves the current fallback chain for every call so an
// updated RPM takes effect without leaving the component on the old bucket.
func withCurrentCandidateRateLimit(
	delegate providers.LLMProvider,
	al *AgentLoop,
	candidates []providers.FallbackCandidate,
) providers.LLMProvider {
	if delegate == nil || al == nil || len(candidates) == 0 {
		return delegate
	}
	return &candidateRateLimitedProvider{
		delegate: delegate,
		wait: func(ctx context.Context, candidate providers.FallbackCandidate) error {
			al.mu.RLock()
			fallback := al.fallback
			al.mu.RUnlock()
			if fallback == nil {
				return nil
			}
			return fallback.WaitForCandidate(ctx, candidate)
		},
		candidate: candidates[0],
	}
}

func (p *candidateRateLimitedProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	options map[string]any,
) (*providers.LLMResponse, error) {
	if err := p.wait(ctx, p.candidate); err != nil {
		return nil, err
	}
	return p.delegate.Chat(ctx, messages, tools, model, options)
}

func (p *candidateRateLimitedProvider) GetDefaultModel() string {
	return p.delegate.GetDefaultModel()
}
