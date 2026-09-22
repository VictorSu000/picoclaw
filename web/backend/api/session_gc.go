package api

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

const (
	// sessionGCInterval is how often orphan session metas are scanned.
	sessionGCInterval = 5 * time.Hour
	// sessionGCMinAge is how old an orphan meta must be before deletion.
	// It must dwarf the "meta written, .jsonl pending" window created by
	// category/preset pre-creation and turn bootstrap (seconds to minutes).
	sessionGCMinAge = 24 * time.Hour
)

// StartSessionGC launches a background goroutine that periodically removes
// orphan .meta.json files (no sibling .jsonl/.archive.jsonl, empty content,
// older than sessionGCMinAge). Idempotent; stopped by Shutdown.
func (h *Handler) StartSessionGC() {
	h.sessionGCMu.Lock()
	defer h.sessionGCMu.Unlock()
	if h.sessionGCStop != nil {
		return
	}
	h.sessionGCStop = make(chan struct{})
	stop := h.sessionGCStop
	go h.runSessionGC(stop)
}

// StopSessionGC stops the background GC goroutine if it is running.
func (h *Handler) StopSessionGC() {
	h.sessionGCMu.Lock()
	defer h.sessionGCMu.Unlock()
	if h.sessionGCStop != nil {
		close(h.sessionGCStop)
		h.sessionGCStop = nil
	}
}

func (h *Handler) runSessionGC(stop <-chan struct{}) {
	// Delay the first pass so startup traffic settles.
	timer := time.NewTimer(sessionGCInterval)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			h.collectOrphanSessionMetas()
			timer.Reset(sessionGCInterval)
		case <-stop:
			return
		}
	}
}

// collectOrphanSessionMetas deletes session .meta.json files that have no
// messages, no summary, no favorite mark, and have been idle past
// sessionGCMinAge. Safe to call directly (used by tests).
func (h *Handler) collectOrphanSessionMetas() int {
	dir, err := h.sessionsDir()
	if err != nil {
		return 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}

	removed := 0
	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta.json") {
			continue
		}
		base := strings.TrimSuffix(entry.Name(), ".meta.json")
		metaPath := filepath.Join(dir, entry.Name())
		jsonlPath := filepath.Join(dir, base+".jsonl")
		archivePath := filepath.Join(dir, base+".archive.jsonl")

		// Structural checks: any sibling message file means the session is real.
		if exists, _ := fileExists(jsonlPath); exists {
			continue
		}
		if exists, _ := fileExists(archivePath); exists {
			continue
		}

		info, statErr := entry.Info()
		if statErr != nil {
			continue
		}

		if meta, metaErr := h.readSessionMeta(metaPath, base); metaErr != nil {
			// Unreadable/corrupt meta: only remove when the file itself is old.
			if now.Sub(info.ModTime()) < sessionGCMinAge {
				continue
			}
		} else {
			if meta.Count != 0 ||
				strings.TrimSpace(meta.Summary) != "" ||
				meta.Favorited {
				continue
			}
			// Prefer meta.UpdatedAt; fall back to file mtime.
			lastTouched := meta.UpdatedAt
			if lastTouched.IsZero() {
				lastTouched = info.ModTime()
			}
			if now.Sub(lastTouched) < sessionGCMinAge {
				continue
			}
		}

		// Re-stat immediately before remove to skip sessions the gateway may
		// have touched between evaluation and deletion (cross-process race).
		fresh, freshErr := os.Stat(metaPath)
		if freshErr != nil {
			continue
		}
		if now.Sub(fresh.ModTime()) < sessionGCMinAge {
			continue
		}
		// A .jsonl may have appeared while we evaluated.
		if exists, _ := fileExists(jsonlPath); exists {
			continue
		}
		if exists, _ := fileExists(archivePath); exists {
			continue
		}

		if err := os.Remove(metaPath); err != nil {
			if !os.IsNotExist(err) {
				logger.DebugC("web", "session gc: remove "+metaPath+": "+err.Error())
			}
			continue
		}
		removed++
		logger.DebugC("web", "session gc: removed orphan meta "+entry.Name())
	}
	if removed > 0 {
		logger.InfoC("web", "session gc: removed "+strconv.Itoa(removed)+" orphan session meta file(s)")
	}
	return removed
}
