package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceMediaStorePersistsAcrossRestart(t *testing.T) {
	workspace := t.TempDir()
	source := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(source, []byte("png-data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	store, err := NewWorkspaceMediaStore(workspace)
	if err != nil {
		t.Fatalf("NewWorkspaceMediaStore: %v", err)
	}
	ref, err := store.Store(source, MediaMeta{
		Filename:    "holiday.png",
		ContentType: "image/png",
		Source:      "pico:upload",
		SessionID:   "session-1",
	}, "pico:pico:session-1:upload:1")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	path, meta, err := store.ResolveWithMeta(ref)
	if err != nil {
		t.Fatalf("ResolveWithMeta: %v", err)
	}
	objectsDir := filepath.Join(workspace, WorkspaceMediaDirName, workspaceMediaObjects)
	if rel, relErr := filepath.Rel(objectsDir, path); relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		t.Fatalf("stored path %q is outside %q", path, objectsDir)
	}
	if meta.SessionID != "session-1" || meta.CleanupPolicy != CleanupPolicyForgetOnly {
		t.Fatalf("unexpected persisted metadata: %+v", meta)
	}

	reopened, err := NewWorkspaceMediaStore(workspace)
	if err != nil {
		t.Fatalf("reopen workspace store: %v", err)
	}
	reopenedPath, reopenedMeta, err := reopened.ResolveWithMeta(ref)
	if err != nil {
		t.Fatalf("resolve after restart: %v", err)
	}
	if reopenedPath != path || reopenedMeta.Filename != "holiday.png" {
		t.Fatalf("reopened media mismatch: path=%q meta=%+v", reopenedPath, reopenedMeta)
	}
	if data, err := os.ReadFile(reopenedPath); err != nil || string(data) != "png-data" {
		t.Fatalf("reopened object data=%q err=%v", data, err)
	}
}

func TestWorkspaceMediaStoreReleaseIsPermanentAndDeleteIsExplicit(t *testing.T) {
	workspace := t.TempDir()
	source := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(source, []byte("report"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	store, err := NewWorkspaceMediaStore(workspace)
	if err != nil {
		t.Fatalf("NewWorkspaceMediaStore: %v", err)
	}
	ref, err := store.Store(source, MediaMeta{Filename: "report.txt"}, "scope-1")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if err := store.ReleaseAll("scope-1"); err != nil {
		t.Fatalf("ReleaseAll: %v", err)
	}
	if _, err := store.Resolve(ref); err != nil {
		t.Fatalf("release invalidated permanent media: %v", err)
	}

	if err := store.Delete(ref); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	reopened, err := NewWorkspaceMediaStore(workspace)
	if err != nil {
		t.Fatalf("reopen workspace store: %v", err)
	}
	if _, err := reopened.Resolve(ref); err == nil {
		t.Fatal("deleted ref resolved after restart")
	}
}
