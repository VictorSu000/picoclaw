package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/fileutil"
	"github.com/sipeed/picoclaw/pkg/memory"
)

// defaultCategoryID is the built-in category for conversations without a
// category field. It is never persisted in categories.json. Its display name
// is provided by the frontend via i18n.
const defaultCategoryID = "default"

// maxCategoryNameRunes bounds user-provided category display names.
const maxCategoryNameRunes = 40

type category struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
}

type categoriesFile struct {
	Categories []category `json:"categories"`
}

type createCategoryRequest struct {
	Name string `json:"name"`
}

type sessionCategoryRequest struct {
	Category string `json:"category"`
}

// registerCategoryRoutes binds category CRUD endpoints to the ServeMux.
func (h *Handler) registerCategoryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/categories", h.handleListCategories)
	mux.HandleFunc("POST /api/categories", h.handleCreateCategory)
	mux.HandleFunc("PUT /api/sessions/{id}/category", h.handleSetSessionCategory)
}

// categoriesPath returns categories.json beside the app config file.
func (h *Handler) categoriesPath() string {
	dir := filepath.Dir(h.configPath)
	if dir == "" || dir == "." {
		dir = "."
	}
	return filepath.Join(dir, "categories.json")
}

// categoriesMu serializes read-modify-write cycles on categories.json.
var categoriesMu sync.Mutex

func (h *Handler) readCategories() ([]category, error) {
	data, err := os.ReadFile(h.categoriesPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var file categoriesFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	return file.Categories, nil
}

func (h *Handler) writeCategories(categories []category) error {
	file := categoriesFile{Categories: categories}
	if file.Categories == nil {
		file.Categories = []category{}
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(h.categoriesPath(), data, 0o644)
}

func newCategoryID() (string, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "cat_" + hex.EncodeToString(buf), nil
}

// handleListCategories returns the default category followed by custom ones.
// The default category's name is empty; the frontend renders it via i18n.
//
//	GET /api/categories
func (h *Handler) handleListCategories(w http.ResponseWriter, r *http.Request) {
	categoriesMu.Lock()
	custom, err := h.readCategories()
	categoriesMu.Unlock()
	if err != nil {
		http.Error(w, "failed to read categories", http.StatusInternalServerError)
		return
	}

	items := make([]category, 0, len(custom)+1)
	items = append(items, category{
		ID:      defaultCategoryID,
		Created: time.Unix(0, 0).UTC(),
	})
	items = append(items, custom...)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

// handleCreateCategory creates a custom category.
//
//	POST /api/categories
//	Request body: {"name": "工作"}
func (h *Handler) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	var req createCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "category name is required", http.StatusBadRequest)
		return
	}
	if len([]rune(name)) > maxCategoryNameRunes {
		http.Error(w, "category name is too long", http.StatusBadRequest)
		return
	}
	if name == "默认" || strings.EqualFold(name, "default") {
		http.Error(w, "category name already exists", http.StatusConflict)
		return
	}

	categoriesMu.Lock()
	defer categoriesMu.Unlock()

	custom, err := h.readCategories()
	if err != nil {
		http.Error(w, "failed to read categories", http.StatusInternalServerError)
		return
	}
	for _, existing := range custom {
		if strings.EqualFold(existing.Name, name) {
			http.Error(w, "category name already exists", http.StatusConflict)
			return
		}
	}

	id, err := newCategoryID()
	if err != nil {
		http.Error(w, "failed to generate category id", http.StatusInternalServerError)
		return
	}

	created := category{ID: id, Name: name, Created: time.Now().UTC()}
	custom = append(custom, created)
	if err := h.writeCategories(custom); err != nil {
		http.Error(w, "failed to save category", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(created)
}

// handleSetSessionCategory moves a session to the given category.
//
//	PUT /api/sessions/{id}/category
//	Request body: {"category": "cat_a1b2c3"} — empty or "default" clears it.
func (h *Handler) handleSetSessionCategory(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	var req sessionCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	categoryID := strings.TrimSpace(req.Category)
	if categoryID != "" && categoryID != defaultCategoryID {
		categoriesMu.Lock()
		custom, err := h.readCategories()
		categoriesMu.Unlock()
		if err != nil {
			http.Error(w, "failed to read categories", http.StatusInternalServerError)
			return
		}
		found := false
		for _, c := range custom {
			if c.ID == categoryID {
				found = true
				break
			}
		}
		if !found {
			http.Error(w, "category not found", http.StatusNotFound)
			return
		}
	} else {
		categoryID = ""
	}

	dir, err := h.sessionsDir()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "failed to create sessions directory", http.StatusInternalServerError)
		return
	}

	key := ""
	var allocationScope []byte
	var allocationAliases []string

	if ref, findErr := h.findPicoJSONLSession(dir, sessionID); findErr == nil {
		key = ref.Key
	} else if !errors.Is(findErr, os.ErrNotExist) {
		http.Error(w, "failed to find session", http.StatusInternalServerError)
		return
	} else if _, legacyErr := h.findLegacyPicoSession(dir, sessionID); legacyErr == nil {
		http.Error(w, "categories are unavailable for legacy sessions", http.StatusConflict)
		return
	} else {
		// Session has no files yet: derive the deterministic key the gateway
		// will use and pre-create meta (same pattern as agent preset set).
		cfg, cfgErr := config.LoadConfig(h.configPath)
		if cfgErr != nil {
			http.Error(w, "failed to load config", http.StatusInternalServerError)
			return
		}
		_, allocation := picoRouteAllocation(cfg, sessionID)
		key = allocation.SessionKey
		scopeData, marshalErr := json.Marshal(allocation.Scope)
		if marshalErr != nil {
			http.Error(w, "failed to encode session scope", http.StatusInternalServerError)
			return
		}
		allocationScope = scopeData
		allocationAliases = allocation.SessionAliases
	}

	store, storeErr := memory.NewJSONLStore(dir)
	if storeErr != nil {
		http.Error(w, "failed to open session store", http.StatusInternalServerError)
		return
	}
	defer store.Close()

	if allocationScope != nil {
		if upsertErr := store.UpsertSessionMeta(
			r.Context(),
			key,
			allocationScope,
			allocationAliases,
		); upsertErr != nil {
			http.Error(w, "failed to initialize session metadata", http.StatusInternalServerError)
			return
		}
	}
	if err := store.SetSessionCategory(r.Context(), key, categoryID); err != nil {
		http.Error(w, "failed to save session category", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"id":       sessionID,
		"category": categoryID,
	})
}
