package memory

import (
	"context"
	"os"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestArchiveMessages_Roundtrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	dropped := []providers.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "second"},
		{Role: "user", Content: "third"},
	}
	if err := store.ArchiveMessages(ctx, "s1", dropped); err != nil {
		t.Fatalf("ArchiveMessages: %v", err)
	}

	got, err := store.ReadArchivedMessages(ctx, "s1")
	if err != nil {
		t.Fatalf("ReadArchivedMessages: %v", err)
	}
	if len(got) != len(dropped) {
		t.Fatalf("expected %d archived messages, got %d", len(dropped), len(got))
	}
	for i := range dropped {
		if got[i].Role != dropped[i].Role || got[i].Content != dropped[i].Content {
			t.Errorf("message %d = %+v, want %+v", i, got[i], dropped[i])
		}
	}
}

func TestReadArchivedMessages_MissingFile(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	got, err := store.ReadArchivedMessages(ctx, "does-not-exist")
	if err != nil {
		t.Fatalf("ReadArchivedMessages on missing file: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty archive, got %d messages", len(got))
	}
}

func TestArchiveMessages_Empty_NoFile(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.ArchiveMessages(ctx, "s1", nil); err != nil {
		t.Fatalf("ArchiveMessages(nil): %v", err)
	}
	if _, err := os.Stat(store.archivePath("s1")); !os.IsNotExist(err) {
		t.Errorf("expected no archive file for empty input, stat err = %v", err)
	}
}

func TestArchiveMessages_AppendsAcrossCalls(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.ArchiveMessages(ctx, "s1", []providers.Message{{Role: "user", Content: "a"}}); err != nil {
		t.Fatalf("ArchiveMessages first: %v", err)
	}
	if err := store.ArchiveMessages(ctx, "s1", []providers.Message{{Role: "assistant", Content: "b"}}); err != nil {
		t.Fatalf("ArchiveMessages second: %v", err)
	}

	got, err := store.ReadArchivedMessages(ctx, "s1")
	if err != nil {
		t.Fatalf("ReadArchivedMessages: %v", err)
	}
	if len(got) != 2 || got[0].Content != "a" || got[1].Content != "b" {
		t.Fatalf("expected [a b] in order, got %+v", got)
	}
}

// TestArchiveMessages_SurvivesActiveCompaction verifies that archiving the
// dropped prefix before truncating the active history preserves it even after
// Compact physically rewrites the active JSONL — the core guarantee behind the
// Web UI showing compacted history.
func TestArchiveMessages_SurvivesActiveCompaction(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Build an active history of 4 messages.
	active := []providers.Message{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
	}
	if err := store.SetHistory(ctx, "s1", active); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	// Archive the oldest two (the prefix compaction would drop), then truncate
	// the active history to the last two and compact.
	if err := store.ArchiveMessages(ctx, "s1", active[:2]); err != nil {
		t.Fatalf("ArchiveMessages: %v", err)
	}
	if err := store.TruncateHistory(ctx, "s1", 2); err != nil {
		t.Fatalf("TruncateHistory: %v", err)
	}
	if err := store.Compact(ctx, "s1"); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// Active history is compacted to the last two messages.
	gotActive, err := store.GetHistory(ctx, "s1")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(gotActive) != 2 || gotActive[0].Content != "u2" || gotActive[1].Content != "a2" {
		t.Fatalf("active history = %+v, want [u2 a2]", gotActive)
	}

	// The archived prefix is untouched by the active-file compaction.
	gotArchive, err := store.ReadArchivedMessages(ctx, "s1")
	if err != nil {
		t.Fatalf("ReadArchivedMessages: %v", err)
	}
	if len(gotArchive) != 2 || gotArchive[0].Content != "u1" || gotArchive[1].Content != "a1" {
		t.Fatalf("archive = %+v, want [u1 a1]", gotArchive)
	}
}
