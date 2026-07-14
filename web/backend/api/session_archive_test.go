package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// TestHandleGetSession_IncludesArchivedHistory verifies that GET returns the
// archived (compaction-dropped) messages prepended to the active history, the
// correct archived_count boundary, and the summary.
func TestHandleGetSession_IncludesArchivedHistory(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}

	sessionKey := legacyPicoSessionPrefix + "archived-jsonl"

	// Active history (what the agent still sees).
	for _, msg := range []providers.Message{
		{Role: "user", Content: "active-user"},
		{Role: "assistant", Content: "active-assistant"},
	} {
		if err := store.AddFullMessage(nil, sessionKey, msg); err != nil {
			t.Fatalf("AddFullMessage() error = %v", err)
		}
	}
	if err := store.SetSummary(nil, sessionKey, "compaction summary"); err != nil {
		t.Fatalf("SetSummary() error = %v", err)
	}

	// Archived history (compaction-dropped, display-only).
	archived := []providers.Message{
		{Role: "user", Content: "old-user"},
		{Role: "assistant", Content: "old-assistant"},
	}
	if err := store.ArchiveMessages(nil, sessionKey, archived); err != nil {
		t.Fatalf("ArchiveMessages() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/archived-jsonl", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Summary       string `json:"summary"`
		ArchivedCount int    `json:"archived_count"`
		Messages      []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if resp.Summary != "compaction summary" {
		t.Fatalf("resp.Summary = %q, want %q", resp.Summary, "compaction summary")
	}
	if resp.ArchivedCount != 2 {
		t.Fatalf("resp.ArchivedCount = %d, want 2", resp.ArchivedCount)
	}
	// Archived messages come first, followed by the active history.
	wantOrder := []string{"old-user", "old-assistant", "active-user", "active-assistant"}
	if len(resp.Messages) != len(wantOrder) {
		t.Fatalf("len(messages) = %d, want %d (%v)", len(resp.Messages), len(wantOrder), resp.Messages)
	}
	for i, want := range wantOrder {
		if resp.Messages[i].Content != want {
			t.Fatalf("message[%d] = %q, want %q", i, resp.Messages[i].Content, want)
		}
	}
}

// TestHandleDeleteSession_RemovesArchive verifies that deleting a session also
// removes its archive file.
func TestHandleDeleteSession_RemovesArchive(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}

	sessionKey := legacyPicoSessionPrefix + "delete-archive"
	if err := store.AddFullMessage(nil, sessionKey, providers.Message{Role: "user", Content: "hi"}); err != nil {
		t.Fatalf("AddFullMessage() error = %v", err)
	}
	if err := store.ArchiveMessages(nil, sessionKey, []providers.Message{{Role: "user", Content: "old"}}); err != nil {
		t.Fatalf("ArchiveMessages() error = %v", err)
	}

	// Sanity: an archive file exists before deletion.
	if countArchiveFiles(t, dir) == 0 {
		t.Fatal("expected an archive file before deletion")
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/delete-archive", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}

	if n := countArchiveFiles(t, dir); n != 0 {
		t.Fatalf("expected archive file removed, found %d", n)
	}
}

func countArchiveFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".archive.jsonl") {
			count++
		}
	}
	return count
}
