package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/memory"
)

func newCategoryTestHandler(t *testing.T) *Handler {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return NewHandler(configPath)
}

func TestHandleListCategories_DefaultFirst(t *testing.T) {
	h := newCategoryTestHandler(t)
	mux := http.NewServeMux()
	h.registerCategoryRoutes(mux)

	req := httptest.NewRequest("GET", "/api/categories", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var items []category
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].ID != defaultCategoryID {
		t.Fatalf("items[0].ID = %q, want %q", items[0].ID, defaultCategoryID)
	}
}

func TestHandleCreateCategory_DuplicateRejected(t *testing.T) {
	h := newCategoryTestHandler(t)
	mux := http.NewServeMux()
	h.registerCategoryRoutes(mux)

	create := func(name string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(createCategoryRequest{Name: name})
		req := httptest.NewRequest("POST", "/api/categories", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	if rec := create("工作"); rec.Code != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if rec := create(" 工作 "); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate create status = %d, want 409", rec.Code)
	}
	if rec := create("默认"); rec.Code != http.StatusConflict {
		t.Fatalf("default-name create status = %d, want 409", rec.Code)
	}
	if rec := create(""); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty name status = %d, want 400", rec.Code)
	}
}

func TestHandleCreateCategory_PersistsBesideConfig(t *testing.T) {
	h := newCategoryTestHandler(t)
	mux := http.NewServeMux()
	h.registerCategoryRoutes(mux)

	body, _ := json.Marshal(createCategoryRequest{Name: "Work"})
	req := httptest.NewRequest("POST", "/api/categories", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}

	// categories.json must sit next to config.json, not under sessions/.
	path := filepath.Join(filepath.Dir(h.configPath), "categories.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("categories.json missing beside config: %v", err)
	}

	var created category
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(created.ID, "cat_") {
		t.Fatalf("id = %q, want cat_ prefix", created.ID)
	}
	if created.Name != "Work" {
		t.Fatalf("name = %q, want Work", created.Name)
	}
}

func TestSessionListCategoryFilter(t *testing.T) {
	// Unit-level: the filter predicate used by handleListSessions.
	items := []sessionListItem{
		{ID: "a", Category: ""},
		{ID: "b", Category: "cat_x"},
		{ID: "c", Category: "cat_x"},
		{ID: "d", Category: "cat_y"},
	}

	filter := func(categoryFilter string) []string {
		var filtered []string
		for i := range items {
			itemCategory := strings.TrimSpace(items[i].Category)
			if categoryFilter != "" {
				if categoryFilter == defaultCategoryID {
					if itemCategory == "" {
						filtered = append(filtered, items[i].ID)
					}
					continue
				}
				if itemCategory != categoryFilter {
					continue
				}
			}
			filtered = append(filtered, items[i].ID)
		}
		return filtered
	}

	if got := filter(""); len(got) != 4 {
		t.Fatalf("no filter = %v, want all 4", got)
	}
	if got := filter(defaultCategoryID); len(got) != 1 || got[0] != "a" {
		t.Fatalf("default filter = %v, want [a]", got)
	}
	if got := filter("cat_x"); len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("cat_x filter = %v, want [b c]", got)
	}
	if got := filter("cat_missing"); len(got) != 0 {
		t.Fatalf("missing filter = %v, want empty", got)
	}
}

func TestSetSessionCategory_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	defer store.Close()

	ctx := t.Context()
	key := "sk_v1_testcat"
	if err := store.SetSessionCategory(ctx, key, "cat_abc"); err != nil {
		t.Fatalf("SetSessionCategory: %v", err)
	}
	meta, err := store.GetSessionMeta(ctx, key)
	if err != nil {
		t.Fatalf("GetSessionMeta: %v", err)
	}
	if meta.Category != "cat_abc" {
		t.Fatalf("Category = %q, want cat_abc", meta.Category)
	}

	// Clear → field empty (default category).
	if err := store.SetSessionCategory(ctx, key, ""); err != nil {
		t.Fatalf("clear category: %v", err)
	}
	meta, err = store.GetSessionMeta(ctx, key)
	if err != nil {
		t.Fatalf("GetSessionMeta: %v", err)
	}
	if meta.Category != "" {
		t.Fatalf("Category = %q, want empty", meta.Category)
	}
}

func TestSessionGC_RemovesOnlyOldEmptyOrphans(t *testing.T) {
	dir := t.TempDir()
	// resolveSessionsDir joins workspace + "sessions".
	sessionsDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	configPath := filepath.Join(dir, "config.json")
	cfg := []byte(`{"agents":{"defaults":{"workspace":"` + dir + `"}}}`)
	if err := os.WriteFile(configPath, cfg, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	h := NewHandler(configPath)

	old := time.Now().Add(-48 * time.Hour)
	writeMeta := func(name string, mutate func(*memory.SessionMeta), modTime time.Time) string {
		meta := memory.SessionMeta{
			Key:       strings.TrimSuffix(name, ".meta.json"),
			Count:     0,
			CreatedAt: old,
			UpdatedAt: old,
		}
		if mutate != nil {
			mutate(&meta)
		}
		data, err := json.MarshalIndent(meta, "", "  ")
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		path := filepath.Join(sessionsDir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("write meta: %v", err)
		}
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
		return path
	}

	// 1. Old empty orphan → should be removed.
	orphan := writeMeta("orphan.meta.json", nil, old)

	// 2. Old empty but favorited → keep.
	fav := writeMeta("fav.meta.json", func(m *memory.SessionMeta) {
		m.Favorited = true
	}, old)

	// 3. Old empty but has summary → keep.
	sum := writeMeta("sum.meta.json", func(m *memory.SessionMeta) {
		m.Summary = "hello"
	}, old)

	// 4. Old empty but sibling .jsonl exists → keep.
	withJSONL := writeMeta("real.meta.json", nil, old)
	if err := os.WriteFile(strings.TrimSuffix(withJSONL, ".meta.json")+".jsonl", []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	// 5. Fresh empty orphan → keep (age threshold).
	fresh := time.Now()
	recent := writeMeta("recent.meta.json", nil, fresh)

	// 6. Old with Count > 0 → keep.
	counted := writeMeta("counted.meta.json", func(m *memory.SessionMeta) {
		m.Count = 3
	}, old)

	removed := h.collectOrphanSessionMetas()
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan meta should be removed, stat err = %v", err)
	}
	for _, keep := range []string{fav, sum, withJSONL, recent, counted} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("expected %s to remain: %v", filepath.Base(keep), err)
		}
	}
}
