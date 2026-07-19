package session_test

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/session"
)

func TestAgentPresetPersistenceJSONLBackend(t *testing.T) {
	dir := t.TempDir()
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	backend := session.NewJSONLBackend(store)
	if err := backend.SetAgentPreset("session-1", "coding"); err != nil {
		t.Fatal(err)
	}
	if got := backend.GetAgentPreset("session-1"); got != "coding" {
		t.Fatalf("GetAgentPreset() = %q, want coding", got)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reloaded := session.NewJSONLBackend(reopened)
	if got := reloaded.GetAgentPreset("session-1"); got != "coding" {
		t.Fatalf("reloaded GetAgentPreset() = %q, want coding", got)
	}
	if err := reloaded.SetAgentPreset("session-1", ""); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.GetAgentPreset("session-1"); got != "" {
		t.Fatalf("reset GetAgentPreset() = %q, want empty", got)
	}
	if _, override := reloaded.GetAgentPresetOverride("session-1"); !override {
		t.Fatal("SetAgentPreset with an empty name should persist an explicit default override")
	}
	if err := reloaded.SetAgentPresetOverride("session-1", "", false); err != nil {
		t.Fatal(err)
	}
	if _, override := reloaded.GetAgentPresetOverride("session-1"); override {
		t.Fatal("cleared JSONL preset override was not persisted")
	}
}

func TestAgentPresetPersistenceLegacySessionManager(t *testing.T) {
	dir := t.TempDir()
	manager := session.NewSessionManager(dir)
	if err := manager.SetAgentPreset("session-1", "research"); err != nil {
		t.Fatal(err)
	}

	reloaded := session.NewSessionManager(dir)
	if got := reloaded.GetAgentPreset("session-1"); got != "research" {
		t.Fatalf("GetAgentPreset() = %q, want research", got)
	}
	if err := reloaded.SetAgentPresetOverride("session-1", "", false); err != nil {
		t.Fatal(err)
	}
	reloaded = session.NewSessionManager(dir)
	if _, override := reloaded.GetAgentPresetOverride("session-1"); override {
		t.Fatal("cleared legacy session preset override was not persisted")
	}
}
