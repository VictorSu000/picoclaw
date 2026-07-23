package media

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sipeed/picoclaw/pkg/logger"
)

const (
	WorkspaceMediaDirName = "media"
	workspaceMediaObjects = "objects"
	workspaceMediaIndex   = "index.jsonl"
)

// WorkspaceMediaStore keeps media objects and their ref metadata under the
// configured workspace. The index is append-only so a process restart can
// rebuild the in-memory lookup table without relying on the old temp process.
type WorkspaceMediaStore struct {
	mu          sync.RWMutex
	root        string
	objectsDir  string
	indexPath   string
	refs        map[string]workspaceMediaEntry
	scopeToRefs map[string]map[string]struct{}
}

type workspaceMediaEntry struct {
	Path     string
	Meta     MediaMeta
	Scope    string
	StoredAt time.Time
}

type workspaceMediaRecord struct {
	Ref      string    `json:"ref"`
	Path     string    `json:"path"`
	Meta     MediaMeta `json:"meta"`
	Scope    string    `json:"scope,omitempty"`
	StoredAt time.Time `json:"stored_at"`
	Deleted  bool      `json:"deleted,omitempty"`
}

// NewWorkspaceMediaStore creates the production media store rooted at
// <workspace>/media. Existing records are loaded before the store is returned.
func NewWorkspaceMediaStore(workspace string) (*WorkspaceMediaStore, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil, fmt.Errorf("media workspace is empty")
	}

	root := filepath.Join(workspace, WorkspaceMediaDirName)
	objectsDir := filepath.Join(root, workspaceMediaObjects)
	if err := os.MkdirAll(objectsDir, 0o700); err != nil {
		return nil, fmt.Errorf("create workspace media directory: %w", err)
	}

	store := &WorkspaceMediaStore{
		root:        root,
		objectsDir:  objectsDir,
		indexPath:   filepath.Join(root, workspaceMediaIndex),
		refs:        make(map[string]workspaceMediaEntry),
		scopeToRefs: make(map[string]map[string]struct{}),
	}
	if err := store.loadIndex(); err != nil {
		return nil, err
	}
	return store, nil
}

// Store copies the source into the workspace media directory and durably
// records its media:// reference. The source is left untouched unless it is a
// managed staging file under media.TempDir(), in which case it is removed after
// the durable record is committed.
func (s *WorkspaceMediaStore) Store(localPath string, meta MediaMeta, scope string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("media store is nil")
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return "", fmt.Errorf("media store: %s: %w", localPath, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("media store: %s is a directory", localPath)
	}

	ref := "media://" + uuid.New().String()
	originalPolicy := normalizeCleanupPolicy(meta.CleanupPolicy)
	ext := workspaceMediaExtension(meta.Filename, localPath)
	objectPath := filepath.Join(s.objectsDir, strings.TrimPrefix(ref, "media://")+ext)
	relPath, err := filepath.Rel(s.root, objectPath)
	if err != nil {
		return "", fmt.Errorf("media store: derive object path: %w", err)
	}

	if err := copyMediaFile(localPath, objectPath, info.Mode().Perm()); err != nil {
		return "", err
	}

	meta.CleanupPolicy = CleanupPolicyForgetOnly
	entry := workspaceMediaEntry{
		Path:     objectPath,
		Meta:     meta,
		Scope:    scope,
		StoredAt: time.Now(),
	}
	record := workspaceMediaRecord{
		Ref:      ref,
		Path:     filepath.ToSlash(relPath),
		Meta:     meta,
		Scope:    scope,
		StoredAt: entry.StoredAt,
	}

	s.mu.Lock()
	if err := s.appendRecordLocked(record); err != nil {
		s.mu.Unlock()
		_ = os.Remove(objectPath)
		return "", err
	}
	s.refs[ref] = entry
	if s.scopeToRefs[scope] == nil {
		s.scopeToRefs[scope] = make(map[string]struct{})
	}
	s.scopeToRefs[scope][ref] = struct{}{}
	s.mu.Unlock()

	if originalPolicy == CleanupPolicyDeleteOnCleanup && isManagedStagingPath(localPath) {
		_ = os.Remove(localPath)
	}
	return ref, nil
}

func (s *WorkspaceMediaStore) Resolve(ref string) (string, error) {
	path, _, err := s.ResolveWithMeta(ref)
	return path, err
}

func (s *WorkspaceMediaStore) ResolveWithMeta(ref string) (string, MediaMeta, error) {
	s.mu.RLock()
	entry, ok := s.refs[ref]
	s.mu.RUnlock()
	if !ok {
		return "", MediaMeta{}, fmt.Errorf("media store: unknown ref: %s", ref)
	}
	if _, err := os.Stat(entry.Path); err != nil {
		return "", MediaMeta{}, fmt.Errorf("media store: %s: %w", entry.Path, err)
	}
	return entry.Path, entry.Meta, nil
}

// ReleaseAll is intentionally a no-op for durable media. Turn/channel
// lifecycle scopes must not invalidate a permanent WebUI download URL.
func (s *WorkspaceMediaStore) ReleaseAll(_ string) error { return nil }

// Delete explicitly removes one durable media object. Automatic lifecycle
// cleanup never calls this method.
func (s *WorkspaceMediaStore) Delete(ref string) error {
	s.mu.Lock()
	entry, ok := s.refs[ref]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("media store: unknown ref: %s", ref)
	}
	if err := s.appendRecordLocked(workspaceMediaRecord{Ref: ref, Deleted: true, StoredAt: time.Now()}); err != nil {
		s.mu.Unlock()
		return err
	}
	delete(s.refs, ref)
	if refs := s.scopeToRefs[entry.Scope]; refs != nil {
		delete(refs, ref)
		if len(refs) == 0 {
			delete(s.scopeToRefs, entry.Scope)
		}
	}
	s.mu.Unlock()

	if err := os.Remove(entry.Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("media store: remove %s: %w", entry.Path, err)
	}
	return nil
}

func (s *WorkspaceMediaStore) loadIndex() error {
	file, err := os.Open(s.indexPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open media index: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var record workspaceMediaRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			logger.WarnCF("media", "Ignoring malformed media index record", map[string]any{"error": err.Error()})
			continue
		}
		if record.Ref == "" {
			continue
		}
		if record.Deleted {
			s.removeLoadedRef(record.Ref)
			continue
		}
		objectPath, err := s.safeObjectPath(record.Path)
		if err != nil {
			logger.WarnCF("media", "Ignoring unsafe media index path", map[string]any{"ref": record.Ref, "error": err.Error()})
			continue
		}
		entry := workspaceMediaEntry{
			Path:     objectPath,
			Meta:     record.Meta,
			Scope:    record.Scope,
			StoredAt: record.StoredAt,
		}
		s.refs[record.Ref] = entry
		if s.scopeToRefs[record.Scope] == nil {
			s.scopeToRefs[record.Scope] = make(map[string]struct{})
		}
		s.scopeToRefs[record.Scope][record.Ref] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read media index: %w", err)
	}
	return nil
}

func (s *WorkspaceMediaStore) removeLoadedRef(ref string) {
	entry, ok := s.refs[ref]
	if !ok {
		return
	}
	delete(s.refs, ref)
	if refs := s.scopeToRefs[entry.Scope]; refs != nil {
		delete(refs, ref)
		if len(refs) == 0 {
			delete(s.scopeToRefs, entry.Scope)
		}
	}
}

func (s *WorkspaceMediaStore) safeObjectPath(relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", fmt.Errorf("media path must be relative")
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("media path escapes workspace media directory")
	}
	objectsPrefix := workspaceMediaObjects + string(os.PathSeparator)
	if !strings.HasPrefix(clean, objectsPrefix) {
		return "", fmt.Errorf("media path is outside objects directory")
	}
	path := filepath.Join(s.root, clean)
	root, err := filepath.Abs(s.root)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("media path escapes workspace media directory")
	}
	return abs, nil
}

func (s *WorkspaceMediaStore) appendRecordLocked(record workspaceMediaRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode media index record: %w", err)
	}
	file, err := os.OpenFile(s.indexPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open media index: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("write media index: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync media index: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close media index: %w", err)
	}
	return nil
}

func copyMediaFile(source, destination string, perm os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open media source: %w", err)
	}
	defer input.Close()

	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create media object: %w", err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		_ = os.Remove(destination)
		return fmt.Errorf("copy media object: %w", err)
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		_ = os.Remove(destination)
		return fmt.Errorf("sync media object: %w", err)
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(destination)
		return fmt.Errorf("close media object: %w", err)
	}
	if perm != 0 {
		_ = os.Chmod(destination, perm&0o600)
	}
	return nil
}

func workspaceMediaExtension(filename, localPath string) string {
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = filepath.Ext(localPath)
	}
	if len(ext) < 2 || len(ext) > 16 {
		return ""
	}
	for _, r := range ext[1:] {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return strings.ToLower(ext)
}

func isManagedStagingPath(path string) bool {
	root, err := filepath.Abs(filepath.Clean(TempDir()))
	if err != nil {
		return false
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}
	return rel != "."
}
