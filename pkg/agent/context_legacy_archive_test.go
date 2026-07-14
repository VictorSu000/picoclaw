package agent

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// readArchiveFile reads the on-disk archive for a session. The filename mirrors
// memory.sanitizeKey, which is an identity mapping for simple keys (no ':' '/'
// '\'). Returns nil when the archive file does not exist.
func readArchiveFile(t *testing.T, workspace, sessionKey string) []providers.Message {
	t.Helper()
	path := filepath.Join(workspace, "sessions", sessionKey+".archive.jsonl")
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer f.Close()

	var msgs []providers.Message
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var m providers.Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("decode archive line: %v", err)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

func sampleHistory() []providers.Message {
	return []providers.Message{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u3"},
		{Role: "assistant", Content: "a3"},
	}
}

// TestLegacyManager_ForceCompressionArchivesDropped verifies that emergency
// compression preserves the dropped prefix in the archive while still shrinking
// the active history (compression behavior unchanged).
func TestLegacyManager_ForceCompressionArchivesDropped(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:     tmpDir,
				ModelName:     "test-model",
				MaxTokens:     1024,
				ContextWindow: 8000,
			},
		},
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), &simpleMockProvider{response: "ok"})
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}

	full := sampleHistory()
	defaultAgent.Sessions.SetHistory("session-1", full)

	lcm := &legacyContextManager{al: al}
	result, ok := lcm.forceCompression("session-1")
	if !ok {
		t.Fatal("expected forceCompression to run")
	}
	if result.DroppedMessages == 0 {
		t.Fatal("expected some dropped messages")
	}

	active := defaultAgent.Sessions.GetHistory("session-1")
	if len(active) >= len(full) {
		t.Fatalf("expected active history to shrink, got %d (was %d)", len(active), len(full))
	}

	archived := readArchiveFile(t, tmpDir, "session-1")
	if len(archived) != result.DroppedMessages {
		t.Fatalf("archived %d messages, expected %d dropped", len(archived), result.DroppedMessages)
	}
	if archived[0].Content != "u1" {
		t.Fatalf("expected archive to start at oldest message u1, got %q", archived[0].Content)
	}
	if len(archived)+len(active) != len(full) {
		t.Fatalf("archive(%d)+active(%d) != full(%d)", len(archived), len(active), len(full))
	}
}

// TestLegacyManager_SummarizeArchivesDropped verifies that summarization
// archives the messages it drops so they remain viewable, while still setting
// the summary and truncating the active history.
func TestLegacyManager_SummarizeArchivesDropped(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:                 tmpDir,
				ModelName:                 "test-model",
				MaxTokens:                 1024,
				ContextWindow:             8000,
				SummarizeMessageThreshold: 2,
				SummarizeTokenPercent:     75,
			},
		},
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), &simpleMockProvider{response: "summary text"})
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}

	full := sampleHistory()
	defaultAgent.Sessions.SetHistory("session-1", full)

	lcm := &legacyContextManager{al: al}
	lcm.summarizeSession(defaultAgent, "session-1")

	if defaultAgent.Sessions.GetSummary("session-1") == "" {
		t.Fatal("expected summary to be set")
	}
	active := defaultAgent.Sessions.GetHistory("session-1")
	if len(active) >= len(full) {
		t.Fatalf("expected active history to shrink, got %d (was %d)", len(active), len(full))
	}

	archived := readArchiveFile(t, tmpDir, "session-1")
	if len(archived) == 0 {
		t.Fatal("expected archived messages after summarization")
	}
	if archived[0].Content != "u1" {
		t.Fatalf("expected archive to start at oldest message u1, got %q", archived[0].Content)
	}
	if len(archived)+len(active) != len(full) {
		t.Fatalf("archive(%d)+active(%d) != full(%d)", len(archived), len(active), len(full))
	}
}
