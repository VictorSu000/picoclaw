package agent

import "testing"

func TestComposeModelLabel(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		requested  string
		upstream   string
		provider   string
		want       string
	}{
		{
			name:       "no upstream model keeps the alias only",
			configured: "my-claude",
			requested:  "anthropic/claude-sonnet-4-5",
			want:       "my-claude",
		},
		{
			name:       "upstream model is appended",
			configured: "my-claude",
			requested:  "dynamic/support",
			upstream:   "claude-sonnet-4-5",
			want:       "my-claude → claude-sonnet-4-5",
		},
		{
			name:       "identical model id is not shown twice",
			configured: "my-claude",
			requested:  "anthropic/claude-sonnet-4-5",
			upstream:   "claude-sonnet-4-5",
			want:       "my-claude",
		},
		{
			name:       "comparison is case insensitive",
			configured: "my-claude",
			requested:  "gpt-4o",
			upstream:   "GPT-4O",
			want:       "my-claude",
		},
		{
			name:       "different model is shown",
			configured: "my-claude",
			requested:  "gpt-4o",
			upstream:   "gpt-4o-2024-08-06",
			want:       "my-claude → gpt-4o-2024-08-06",
		},
		{
			name:       "provider is rendered as a prefix",
			configured: "my-claude",
			requested:  "dynamic/support",
			upstream:   "claude-sonnet-4-5",
			provider:   "anthropic",
			want:       "my-claude → anthropic/claude-sonnet-4-5",
		},
		{
			name:       "provider is not repeated when the model already carries it",
			configured: "my-gpt",
			requested:  "dynamic/support",
			upstream:   "openai/gpt-4o",
			provider:   "openai",
			want:       "my-gpt → openai/gpt-4o",
		},
		{
			name:       "provider-only route is still shown",
			configured: "my-claude",
			requested:  "claude-sonnet-4-5",
			provider:   "anthropic",
			want:       "my-claude → anthropic",
		},
		{
			name:       "identical model id with provider is not shown twice",
			configured: "my-gpt",
			requested:  "gpt-4o",
			upstream:   "gpt-4o",
			provider:   "openai",
			want:       "my-gpt",
		},
		{
			name:       "missing alias falls back to the upstream model",
			configured: "",
			requested:  "dynamic/support",
			upstream:   "claude-sonnet-4-5",
			want:       "claude-sonnet-4-5",
		},
		{
			name:       "already composed alias is not composed twice",
			configured: "my-claude → claude-sonnet-4-5",
			requested:  "dynamic/support",
			upstream:   "claude-opus-4-1",
			want:       "claude-opus-4-1",
		},
		{
			name:       "whitespace only upstream is ignored",
			configured: "my-claude",
			requested:  "gpt-4o",
			upstream:   "   ",
			want:       "my-claude",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := composeModelLabel(tc.configured, tc.requested, tc.upstream, tc.provider)
			if got != tc.want {
				t.Fatalf("composeModelLabel = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTurnExecutionModelLabel(t *testing.T) {
	exec := &turnExecution{llmModel: "dynamic/support"}
	exec.setModelName("my-claude")
	if exec.modelLabel() != "my-claude" {
		t.Fatalf("modelLabel after setModelName = %q, want %q", exec.modelLabel(), "my-claude")
	}

	exec.recordUpstreamRoute("claude-sonnet-4-5", "anthropic")
	if exec.modelLabel() != "my-claude → anthropic/claude-sonnet-4-5" {
		t.Fatalf("modelLabel after recordUpstreamModel = %q", exec.modelLabel())
	}
	// llmModelName must stay the plain alias: it is compared against resolved
	// config model names during media routing.
	if exec.llmModelName != "my-claude" {
		t.Fatalf("llmModelName = %q, want %q", exec.llmModelName, "my-claude")
	}

	// Switching models must drop the previous upstream routing info.
	exec.setModelName("my-gpt")
	if exec.modelLabel() != "my-gpt" {
		t.Fatalf("modelLabel after model switch = %q, want %q", exec.modelLabel(), "my-gpt")
	}
	if exec.llmUpstreamModel != "" {
		t.Fatalf("llmUpstreamModel = %q, want empty", exec.llmUpstreamModel)
	}
	if exec.llmUpstreamProvider != "" {
		t.Fatalf("llmUpstreamProvider = %q, want empty", exec.llmUpstreamProvider)
	}
}

func TestStreamingChunkPublisherUpstreamModel(t *testing.T) {
	streamer := &modelNameRecordingStreamer{}
	publisher := &streamingChunkPublisher{
		streamer:   streamer,
		aliasName:  "my-claude",
		requestedM: "dynamic/support",
		modelName:  "my-claude",
	}

	publisher.noteUpstreamRoute("", "")
	if publisher.modelName != "my-claude" {
		t.Fatalf("modelName = %q, want %q", publisher.modelName, "my-claude")
	}

	publisher.noteUpstreamRoute("claude-sonnet-4-5", "anthropic")
	if publisher.modelName != "my-claude → anthropic/claude-sonnet-4-5" {
		t.Fatalf("modelName = %q", publisher.modelName)
	}
	if publisher.upstreamModel() != "claude-sonnet-4-5" {
		t.Fatalf("upstreamModel = %q", publisher.upstreamModel())
	}
	if publisher.upstreamProvider() != "anthropic" {
		t.Fatalf("upstreamProvider = %q", publisher.upstreamProvider())
	}

	publisher.Update(t.Context(), "hello")
	if got := streamer.lastModelName(); got != "my-claude → anthropic/claude-sonnet-4-5" {
		t.Fatalf("streamer model name = %q", got)
	}
}

type modelNameRecordingStreamer struct {
	recordingStreamer
	names []string
}

func (s *modelNameRecordingStreamer) SetModelName(name string) {
	s.names = append(s.names, name)
}

func (s *modelNameRecordingStreamer) lastModelName() string {
	if len(s.names) == 0 {
		return ""
	}
	return s.names[len(s.names)-1]
}
