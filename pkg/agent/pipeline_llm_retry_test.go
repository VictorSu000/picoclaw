package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/providers/common"
)

func TestTransientLLMRetryReason(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantReason string
		wantRetry  bool
	}{
		{
			name:       "in-band upstream error with string code",
			err:        common.NewInBandAPIError("upstream_error", "upstream_error", "upstream_error"),
			wantReason: "server_error",
			wantRetry:  true,
		},
		{
			name:       "in-band upstream error with nil code",
			err:        common.NewInBandAPIError(nil, "upstream failure", "upstream_error"),
			wantReason: "server_error",
			wantRetry:  true,
		},
		{
			name:       "in-band numeric 503 keeps status mapping",
			err:        common.NewInBandAPIError(503, "service unavailable", "api_error"),
			wantReason: "server_error",
			wantRetry:  true,
		},
		{
			name:       "in-band 429 maps to rate limit",
			err:        common.NewInBandAPIError(429, "too many requests", "rate_limit_error"),
			wantReason: "rate_limit",
			wantRetry:  true,
		},
		{
			name:       "in-band 401 is not retried",
			err:        common.NewInBandAPIError(401, "invalid api key", "authentication_error"),
			wantReason: "",
			wantRetry:  false,
		},
		{
			name:       "in-band unclassified error is not retried",
			err:        common.NewInBandAPIError("bad_prompt", "unsupported parameter", "invalid_request_error"),
			wantReason: "",
			wantRetry:  false,
		},
		{
			name:       "in-band unknown vendor code is retried",
			err:        common.NewInBandAPIError("acme_gateway_failure", "could not be served", "acme_error"),
			wantReason: "server_error",
			wantRetry:  true,
		},
		{
			name:       "in-band out-of-range numeric code is retried",
			err:        common.NewInBandAPIError(42, "unknown", "vendor_error"),
			wantReason: "server_error",
			wantRetry:  true,
		},
		{
			name:       "context deadline exceeded",
			err:        fmt.Errorf("request failed: context deadline exceeded"),
			wantReason: "timeout",
			wantRetry:  true,
		},
		{
			name:       "connection refused",
			err:        fmt.Errorf("dial tcp 127.0.0.1:443: connect: connection refused"),
			wantReason: "network",
			wantRetry:  true,
		},
		{
			name:       "unknown error is not retried",
			err:        fmt.Errorf("something went sideways"),
			wantReason: "",
			wantRetry:  false,
		},
		{
			name:       "nil error is not retried",
			err:        nil,
			wantReason: "",
			wantRetry:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, retry := transientLLMRetryReason(tt.err)
			if retry != tt.wantRetry {
				t.Fatalf("retry = %v, want %v (reason %q)", retry, tt.wantRetry, reason)
			}
			if reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", reason, tt.wantReason)
			}
		})
	}
}

func TestIsInBandGatewayError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "in-band with non-numeric code",
			err:  common.NewInBandAPIError("upstream_error", "upstream_error", "upstream_error"),
			want: true,
		},
		{
			name: "in-band with out-of-range numeric code",
			err:  common.NewInBandAPIError(42, "unknown", "vendor_error"),
			want: true,
		},
		{
			name: "in-band 408 stays a client timeout",
			err:  common.NewInBandAPIError(408, "request timeout", "timeout_error"),
			want: false,
		},
		{
			name: "wrapped in-band with non-numeric code",
			err: fmt.Errorf("failed to parse JSON response: %w",
				common.NewInBandAPIError("upstream_error", "m", "upstream_error")),
			want: true,
		},
		{
			name: "plain error",
			err:  fmt.Errorf("connection reset by peer"),
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isInBandGatewayError(tt.err); got != tt.want {
				t.Errorf("isInBandGatewayError() = %v, want %v", got, tt.want)
			}
		})
	}
}

// inBandFailCountProvider fails the first N calls with an in-band error, then
// succeeds. It exercises the real retry loop in Pipeline.CallLLM.
type inBandFailCountProvider struct {
	err       error
	failCount int
	callCount int
	mu        sync.Mutex
}

func (p *inBandFailCountProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.callCount++
	callCount := p.callCount
	p.mu.Unlock()

	if callCount <= p.failCount {
		return nil, p.err
	}
	return &providers.LLMResponse{Content: "recovered", FinishReason: "stop"}, nil
}

func (p *inBandFailCountProvider) GetDefaultModel() string {
	return "in-band-model"
}

func (p *inBandFailCountProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callCount
}

// nilResponseProvider returns (nil, nil) for the first returnNilFor calls,
// modelling a provider that fails to produce a completion without reporting an
// error.
type nilResponseProvider struct {
	returnNilFor int
	callCount    int
	mu           sync.Mutex
}

func (p *nilResponseProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.callCount++
	callCount := p.callCount
	p.mu.Unlock()

	if callCount <= p.returnNilFor {
		return nil, nil
	}
	return &providers.LLMResponse{Content: "recovered", FinishReason: "stop"}, nil
}

func (p *nilResponseProvider) GetDefaultModel() string {
	return "nil-response-model"
}

func (p *nilResponseProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callCount
}

// newRetryTestPipeline wires a single-candidate pipeline with a short retry
// budget so the LLM retry loop can be driven directly from a test.
func newRetryTestPipeline(t *testing.T, provider providers.LLMProvider) (*Pipeline, *turnState, *turnExecution) {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:           t.TempDir(),
				ModelName:           "test-model",
				MaxTokens:           4096,
				MaxToolIterations:   10,
				MaxLLMRetries:       1,
				LLMRetryBackoffSecs: 1,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, provider)
	t.Cleanup(al.Close)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	pipeline := NewPipeline(al)
	ts := newTurnState(agent, makeTestProcessOpts("test-session"), turnEventScope{
		turnID:  "turn-1",
		context: newTurnContext(nil, nil, nil),
	})
	exec, err := pipeline.SetupTurn(context.Background(), ts)
	if err != nil {
		t.Fatalf("SetupTurn failed: %v", err)
	}
	return pipeline, ts, exec
}

// A gateway reporting a failure inside an HTTP 200 body used to bypass the
// retry loop whenever the error carried no mappable status code. Every in-band
// failure must now be retried, including codes this build has never seen.
func TestPipeline_CallLLM_InBandErrorAlwaysRetried(t *testing.T) {
	tests := []struct {
		name string
		err  *common.InBandAPIError
	}{
		{"upstream_error", common.NewInBandAPIError("upstream_error", "upstream_error", "upstream_error")},
		{"unknown vendor code", common.NewInBandAPIError("acme_failure_9000", "could not be served", "acme_error")},
		{"nil code", common.NewInBandAPIError(nil, "the backend fell over", "internal_failure")},
		{"out of range numeric code", common.NewInBandAPIError(42, "unknown", "vendor_error")},
		{"empty code", common.NewInBandAPIError("", "no reason given", "opaque")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &inBandFailCountProvider{err: tt.err, failCount: 1}
			pipeline, ts, exec := newRetryTestPipeline(t, provider)

			ctrl, err := pipeline.CallLLM(context.Background(), context.Background(), ts, exec, 1)
			if err != nil {
				t.Fatalf("expected retry to recover, got: %v", err)
			}
			if ctrl != ControlBreak {
				t.Fatalf("expected ControlBreak, got %v", ctrl)
			}
			if exec.finalContent != "recovered" {
				t.Fatalf("finalContent = %q, want %q", exec.finalContent, "recovered")
			}
			if got := provider.calls(); got != 2 {
				t.Fatalf("callCount = %d, want 2 (one failure + one retry)", got)
			}
		})
	}
}

// Failures a plain resend cannot fix must not burn the retry budget.
func TestPipeline_CallLLM_InBandPermanentErrorNotRetried(t *testing.T) {
	tests := []struct {
		name string
		err  *common.InBandAPIError
	}{
		{"bad request", common.NewInBandAPIError("bad_prompt", "unsupported parameter", "invalid_request_error")},
		{"content policy", common.NewInBandAPIError("content_filter", "blocked by content policy", "content_filter_error")},
		{"auth", common.NewInBandAPIError("bad_key", "invalid api key", "authentication_error")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &inBandFailCountProvider{err: tt.err, failCount: 10}
			pipeline, ts, exec := newRetryTestPipeline(t, provider)

			if _, err := pipeline.CallLLM(context.Background(), context.Background(), ts, exec, 1); err == nil {
				t.Fatal("expected the permanent failure to surface")
			}
			if got := provider.calls(); got != 1 {
				t.Fatalf("callCount = %d, want 1 (no retry for a permanent failure)", got)
			}
		})
	}
}

// A provider that returns (nil, nil) produced no reply at all; that must be
// treated as an empty response and retried rather than returned to the user.
func TestPipeline_CallLLM_NilResponseRetried(t *testing.T) {
	provider := &nilResponseProvider{returnNilFor: 1}
	pipeline, ts, exec := newRetryTestPipeline(t, provider)

	ctrl, err := pipeline.CallLLM(context.Background(), context.Background(), ts, exec, 1)
	if err != nil {
		t.Fatalf("expected nil response to be retried, got: %v", err)
	}
	if ctrl != ControlBreak {
		t.Fatalf("expected ControlBreak, got %v", ctrl)
	}
	if exec.finalContent != "recovered" {
		t.Fatalf("finalContent = %q, want %q", exec.finalContent, "recovered")
	}
	if got := provider.calls(); got != 2 {
		t.Fatalf("callCount = %d, want 2 (one nil response + one retry)", got)
	}
}

// Once retries are exhausted the error must surface with an accurate attempt
// count instead of the previous hardcoded "after retries" wording.
func TestPipeline_CallLLM_RetryErrorReportsAttemptCount(t *testing.T) {
	provider := &inBandFailCountProvider{
		err:       common.NewInBandAPIError("upstream_error", "upstream_error", "upstream_error"),
		failCount: 10,
	}
	pipeline, ts, exec := newRetryTestPipeline(t, provider)

	_, err := pipeline.CallLLM(context.Background(), context.Background(), ts, exec, 1)
	if err == nil {
		t.Fatal("expected error after retries are exhausted")
	}
	if got := provider.calls(); got != 2 {
		t.Fatalf("callCount = %d, want 2 (initial + 1 retry)", got)
	}
	if !strings.Contains(err.Error(), "after 2 attempts") {
		t.Errorf("error = %q, want it to report the attempt count", err.Error())
	}
}
