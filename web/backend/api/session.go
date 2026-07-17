package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	agentpkg "github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/fileutil"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/providers/messageutil"
	"github.com/sipeed/picoclaw/pkg/routing"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/utils"
)

// registerSessionRoutes binds session list and detail endpoints to the ServeMux.
func (h *Handler) registerSessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions", h.handleListSessions)
	mux.HandleFunc("GET /api/sessions/{id}", h.handleGetSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", h.handleDeleteSession)
	mux.HandleFunc("DELETE /api/sessions/{id}/message-series", h.handleDeleteMessageSeries)
	mux.HandleFunc("POST /api/sessions/{id}/favorite", h.handleFavoriteSession)
	mux.HandleFunc("DELETE /api/sessions/{id}/favorite", h.handleUnfavoriteSession)
	mux.HandleFunc("POST /api/sessions/{id}/fork", h.handleForkSession)
	mux.HandleFunc("POST /api/sessions/{id}/rename", h.handleRenameSession)
}

// sessionFile mirrors the on-disk session JSON structure from pkg/session.
type sessionFile struct {
	Key         string              `json:"key"`
	AgentPreset string              `json:"agent_preset,omitempty"`
	Messages    []providers.Message `json:"messages"`
	Summary     string              `json:"summary,omitempty"`
	Created     time.Time           `json:"created"`
	Updated     time.Time           `json:"updated"`

	// ArchivedRawCount is the number of leading Messages that were dropped from
	// the active history by context compaction and restored from the archive
	// file for display. These occupy Messages[:ArchivedRawCount]. Internal only
	// (never (de)serialized), so legacy JSON sessions leave it at zero.
	ArchivedRawCount int `json:"-"`
}

// sessionListItem is a lightweight summary returned by GET /api/sessions.
type sessionListItem struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Preview      string `json:"preview"`
	MessageCount int    `json:"message_count"`
	Created      string `json:"created"`
	Updated      string `json:"updated"`
	IsFavorited  bool   `json:"is_favorited"`
}

type sessionChatMessage struct {
	Role        string                  `json:"role"`
	Content     string                  `json:"content"`
	Kind        string                  `json:"kind,omitempty"`
	ModelName   string                  `json:"model_name,omitempty"`
	CreatedAt   *time.Time              `json:"created_at,omitempty"`
	Media       []string                `json:"media,omitempty"`
	Attachments []sessionChatAttachment `json:"attachments,omitempty"`
	ToolCalls   []utils.VisibleToolCall `json:"tool_calls,omitempty"`
}

type sessionChatAttachment struct {
	Type        string `json:"type,omitempty"`
	URL         string `json:"url,omitempty"`
	Filename    string `json:"filename,omitempty"`
	ContentType string `json:"content_type,omitempty"`
}

type sessionDetailResponse struct {
	ID             string               `json:"id"`
	Messages       []sessionChatMessage `json:"messages"`
	Summary        string               `json:"summary"`
	AgentPreset    string               `json:"agent_preset,omitempty"`
	EffectiveModel string               `json:"effective_model,omitempty"`
	ArchivedCount  int                  `json:"archived_count"`
	Created        string               `json:"created"`
	Updated        string               `json:"updated"`
}

// legacyPicoSessionPrefix is the legacy key prefix used by older Pico JSON/JSONL
// sessions before structured scope metadata existed.
const (
	legacyPicoSessionPrefix = "agent:main:pico:direct:pico:"
	picoSessionPrefix       = legacyPicoSessionPrefix

	// Keep the session API aligned with the shared JSONL store reader limit in
	// pkg/memory/jsonl.go so oversized lines fail consistently everywhere.
	maxSessionJSONLLineSize = 10 * 1024 * 1024
	maxSessionTitleRunes    = 60

	handledToolResponseSummaryText = "Requested output delivered via tool attachment."
)

func defaultToolFeedbackMaxArgsLength() int {
	defaults := config.AgentDefaults{}
	return defaults.GetToolFeedbackMaxArgsLength()
}

// extractLegacyPicoSessionID extracts the session UUID from an old Pico key.
// Returns the UUID and true if the key matches the Pico session pattern.
func extractLegacyPicoSessionID(key string) (string, bool) {
	if strings.HasPrefix(key, legacyPicoSessionPrefix) {
		return strings.TrimPrefix(key, legacyPicoSessionPrefix), true
	}
	return "", false
}

func sanitizeSessionKey(key string) string {
	key = strings.ReplaceAll(key, ":", "_")
	key = strings.ReplaceAll(key, "/", "_")
	key = strings.ReplaceAll(key, "\\", "_")
	return key
}

func (h *Handler) readLegacySession(path string) (sessionFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sessionFile{}, err
	}

	var sess sessionFile
	if err := json.Unmarshal(data, &sess); err != nil {
		return sessionFile{}, err
	}
	return sess, nil
}

func (h *Handler) readSessionMeta(path, sessionKey string) (memory.SessionMeta, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return memory.SessionMeta{Key: sessionKey}, nil
	}
	if err != nil {
		return memory.SessionMeta{}, err
	}

	var meta memory.SessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return memory.SessionMeta{}, err
	}
	if meta.Key == "" {
		meta.Key = sessionKey
	}
	return meta, nil
}

func (h *Handler) readSessionMessages(path string, skip int) ([]providers.Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	msgs := make([]providers.Message, 0)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSessionJSONLLineSize)

	seen := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		seen++
		if seen <= skip {
			continue
		}

		var msg providers.Message
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if messageutil.IsTransientAssistantThoughtMessage(msg) {
			continue
		}
		msgs = append(msgs, msg)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return msgs, nil
}

// readArchivedSessionMessages reads the session's archive file (messages dropped
// from active history by context compaction). Returns an empty slice when the
// file does not exist. Mirrors readSessionMessages but reads from line 0 since
// the archive has no skip offset.
func (h *Handler) readArchivedSessionMessages(path string) ([]providers.Message, error) {
	msgs, err := h.readSessionMessages(path, 0)
	if os.IsNotExist(err) {
		return []providers.Message{}, nil
	}
	if err != nil {
		return nil, err
	}
	return msgs, nil
}

func (h *Handler) readJSONLSession(dir, sessionKey string) (sessionFile, error) {
	base := filepath.Join(dir, sanitizeSessionKey(sessionKey))
	jsonlPath := base + ".jsonl"
	metaPath := base + ".meta.json"
	archivePath := base + ".archive.jsonl"

	meta, err := h.readSessionMeta(metaPath, sessionKey)
	if err != nil {
		return sessionFile{}, err
	}

	activeMessages, err := h.readSessionMessages(jsonlPath, meta.Skip)
	if os.IsNotExist(err) {
		activeMessages = []providers.Message{}
	} else if err != nil {
		return sessionFile{}, err
	}

	// Prepend archived (compaction-dropped) messages so the Web UI shows the
	// full conversation. The archive is chronological and precedes the active
	// history. The agent loop never reads this file, so these messages stay
	// out of the LLM context.
	archived, err := h.readArchivedSessionMessages(archivePath)
	if err != nil {
		return sessionFile{}, err
	}
	messages := make([]providers.Message, 0, len(archived)+len(activeMessages))
	messages = append(messages, archived...)
	messages = append(messages, activeMessages...)

	updated := meta.UpdatedAt
	created := meta.CreatedAt
	if created.IsZero() || updated.IsZero() {
		if info, statErr := os.Stat(jsonlPath); statErr == nil {
			if created.IsZero() {
				created = info.ModTime()
			}
			if updated.IsZero() {
				updated = info.ModTime()
			}
		}
	}

	return sessionFile{
		Key:              meta.Key,
		AgentPreset:      meta.AgentPreset,
		Messages:         messages,
		Summary:          meta.Summary,
		Created:          created,
		Updated:          updated,
		ArchivedRawCount: len(archived),
	}, nil
}

type picoJSONLSessionRef struct {
	ID  string
	Key string
}

type picoLegacySessionRef struct {
	ID   string
	Path string
}

func extractPicoSessionIDFromScope(scope session.SessionScope) (string, bool) {
	if !strings.EqualFold(strings.TrimSpace(scope.Channel), "pico") {
		return "", false
	}

	candidates := []string{
		strings.TrimSpace(scope.Values["sender"]),
		strings.TrimSpace(scope.Values["chat"]),
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if idx := strings.Index(candidate, "pico:"); idx >= 0 {
			sessionID := strings.TrimSpace(candidate[idx+len("pico:"):])
			if sessionID != "" {
				return sessionID, true
			}
		}
	}
	return "", false
}

func sessionRefFromMeta(meta memory.SessionMeta) (picoJSONLSessionRef, bool) {
	if len(meta.Scope) == 0 {
		if sessionID, ok := extractLegacyPicoSessionID(meta.Key); ok {
			return picoJSONLSessionRef{ID: sessionID, Key: meta.Key}, true
		}
		for _, alias := range meta.Aliases {
			if sessionID, ok := extractLegacyPicoSessionID(alias); ok {
				return picoJSONLSessionRef{ID: sessionID, Key: meta.Key}, true
			}
		}
		return picoJSONLSessionRef{}, false
	}
	var scope session.SessionScope
	if err := json.Unmarshal(meta.Scope, &scope); err != nil {
		return picoJSONLSessionRef{}, false
	}
	sessionID, ok := extractPicoSessionIDFromScope(scope)
	if !ok {
		if legacySessionID, ok := extractLegacyPicoSessionID(meta.Key); ok {
			return picoJSONLSessionRef{ID: legacySessionID, Key: meta.Key}, true
		}
		for _, alias := range meta.Aliases {
			if legacySessionID, ok := extractLegacyPicoSessionID(alias); ok {
				return picoJSONLSessionRef{ID: legacySessionID, Key: meta.Key}, true
			}
		}
		return picoJSONLSessionRef{}, false
	}
	return picoJSONLSessionRef{ID: sessionID, Key: meta.Key}, true
}

func (h *Handler) findPicoJSONLSessions(dir string) ([]picoJSONLSessionRef, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	refs := make([]picoJSONLSessionRef, 0)
	seen := make(map[string]struct{})
	metaBackedBases := make(map[string]struct{})
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta.json") {
			continue
		}
		name := entry.Name()
		metaPath := filepath.Join(dir, name)
		meta, err := h.readSessionMeta(metaPath, "")
		if err != nil {
			continue
		}
		ref, ok := sessionRefFromMeta(meta)
		if !ok || ref.Key == "" || ref.ID == "" {
			continue
		}
		metaBackedBases[strings.TrimSuffix(name, ".meta.json")] = struct{}{}
		if _, exists := seen[ref.ID]; exists {
			continue
		}
		seen[ref.ID] = struct{}{}
		refs = append(refs, ref)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		name := entry.Name()
		base := strings.TrimSuffix(name, ".jsonl")
		if _, ok := metaBackedBases[base]; ok {
			continue
		}
		ref, ok := jsonlSessionRefFromFilename(name)
		if !ok || ref.Key == "" || ref.ID == "" {
			continue
		}
		if _, exists := seen[ref.ID]; exists {
			continue
		}
		seen[ref.ID] = struct{}{}
		refs = append(refs, ref)
	}
	return refs, nil
}

func (h *Handler) findPicoJSONLSession(dir, sessionID string) (picoJSONLSessionRef, error) {
	refs, err := h.findPicoJSONLSessions(dir)
	if err != nil {
		return picoJSONLSessionRef{}, err
	}
	for _, ref := range refs {
		if ref.ID == sessionID {
			return ref, nil
		}
	}
	return picoJSONLSessionRef{}, os.ErrNotExist
}

func (h *Handler) findLegacyPicoSessions(dir string) ([]picoLegacySessionRef, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	refs := make([]picoLegacySessionRef, 0)
	seen := make(map[string]struct{})
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".json" || strings.HasSuffix(name, ".meta.json") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		sess, err := h.readLegacySession(path)
		if err != nil {
			continue
		}

		sessionID, ok := extractLegacyPicoSessionID(sess.Key)
		if !ok || sessionID == "" {
			continue
		}
		if _, exists := seen[sessionID]; exists {
			continue
		}
		seen[sessionID] = struct{}{}
		refs = append(refs, picoLegacySessionRef{ID: sessionID, Path: path})
	}
	return refs, nil
}

func jsonlSessionRefFromFilename(name string) (picoJSONLSessionRef, bool) {
	// Archive files are sidecars of active sessions, not standalone sessions.
	// Without this check, the legacy filename fallback interprets
	// "<session>.archive.jsonl" as a session whose ID ends in ".archive".
	if !strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".archive.jsonl") {
		return picoJSONLSessionRef{}, false
	}
	base := strings.TrimSuffix(name, ".jsonl")
	if base == "" {
		return picoJSONLSessionRef{}, false
	}

	legacyPrefix := sanitizeSessionKey(legacyPicoSessionPrefix)
	if strings.HasPrefix(base, legacyPrefix) {
		sessionID := strings.TrimPrefix(base, legacyPrefix)
		if sessionID == "" {
			return picoJSONLSessionRef{}, false
		}
		return picoJSONLSessionRef{
			ID:  sessionID,
			Key: legacyPicoSessionPrefix + sessionID,
		}, true
	}

	if session.IsOpaqueSessionKey(base) {
		return picoJSONLSessionRef{
			ID:  base,
			Key: base,
		}, true
	}

	return picoJSONLSessionRef{}, false
}

func (h *Handler) findLegacyPicoSession(dir, sessionID string) (picoLegacySessionRef, error) {
	refs, err := h.findLegacyPicoSessions(dir)
	if err != nil {
		return picoLegacySessionRef{}, err
	}
	for _, ref := range refs {
		if ref.ID == sessionID {
			return ref, nil
		}
	}
	return picoLegacySessionRef{}, os.ErrNotExist
}

func buildSessionListItem(sessionID string, sess sessionFile, meta memory.SessionMeta, toolFeedbackMaxArgsLength int) sessionListItem {
	transcript := visibleSessionMessages(sess.Messages, toolFeedbackMaxArgsLength)

	preview := ""
	for _, msg := range transcript {
		if msg.Role == "user" {
			preview = sessionChatMessagePreview(msg)
		}
		if preview != "" {
			break
		}
	}
	preview = truncateRunes(preview, maxSessionTitleRunes)

	if preview == "" {
		preview = "(empty)"
	}

	title := preview
	if strings.TrimSpace(meta.Title) != "" {
		title = meta.Title
	}

	return sessionListItem{
		ID:           sessionID,
		Title:        title,
		Preview:      preview,
		MessageCount: len(transcript),
		Created:      sess.Created.Format(time.RFC3339),
		Updated:      sess.Updated.Format(time.RFC3339),
		IsFavorited:  meta.Favorited,
	}
}

func isEmptySession(sess sessionFile) bool {
	return len(sess.Messages) == 0 && strings.TrimSpace(sess.Summary) == ""
}

func truncateRunes(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= maxLen {
		return string(runes)
	}
	return string(runes[:maxLen]) + "..."
}

func sessionChatMessageVisible(msg sessionChatMessage) bool {
	return strings.TrimSpace(msg.Content) != "" ||
		len(msg.Media) > 0 ||
		len(msg.Attachments) > 0 ||
		len(msg.ToolCalls) > 0
}

func sessionChatMessagePreview(msg sessionChatMessage) string {
	if content := strings.TrimSpace(msg.Content); content != "" {
		return content
	}
	if len(msg.Attachments) > 0 {
		if strings.EqualFold(strings.TrimSpace(msg.Attachments[0].Type), "image") {
			return "[image]"
		}
		return "[attachment]"
	}
	if len(msg.Media) > 0 {
		if strings.HasPrefix(strings.TrimSpace(msg.Media[0]), "data:image/") {
			return "[image]"
		}
		return "[attachment]"
	}
	if len(msg.ToolCalls) > 0 {
		return "[tool call]"
	}
	return ""
}

func visibleSessionMessages(messages []providers.Message, toolFeedbackMaxArgsLength int) []sessionChatMessage {
	return sessionTranscriptMessages(messages, toolFeedbackMaxArgsLength, false)
}

func detailSessionMessages(messages []providers.Message, toolFeedbackMaxArgsLength int) []sessionChatMessage {
	return sessionTranscriptMessages(messages, toolFeedbackMaxArgsLength, true)
}

func sessionTranscriptMessages(
	messages []providers.Message,
	toolFeedbackMaxArgsLength int,
	includeThoughts bool,
) []sessionChatMessage {
	transcript := make([]sessionChatMessage, 0, len(messages))

	for _, msg := range messages {
		attachments := sessionAttachments(msg)

		switch msg.Role {
		case "tool":
			continue

		case "user":
			chatMsg := sessionChatMessage{
				Role:        "user",
				Content:     msg.Content,
				ModelName:   msg.ModelName,
				CreatedAt:   msg.CreatedAt,
				Media:       append([]string(nil), msg.Media...),
				Attachments: attachments,
			}
			if sessionChatMessageVisible(chatMsg) {
				transcript = append(transcript, chatMsg)
			}

		case "assistant":
			if messageutil.IsTransientAssistantThoughtMessage(msg) {
				continue
			}
			if includeThoughts {
				if thoughtMsg, ok := assistantThoughtMessage(msg); ok {
					transcript = append(transcript, thoughtMsg)
				}
			}

			toolCallsMsg, hasToolCallsMsg := assistantToolCallsMessage(
				msg.ToolCalls,
				msg.ModelName,
				toolFeedbackMaxArgsLength,
				msg.CreatedAt,
			)
			visibleToolMessages := visibleAssistantToolMessages(msg.ToolCalls, msg.ModelName, msg.CreatedAt)

			// Pico web chat can persist both visible `message` tool output and a
			// later plain assistant reply in the same turn. Hide only the fixed
			// internal summary that marks handled tool delivery.
			content := msg.Content
			if assistantMessageInternalOnly(msg) {
				if len(attachments) == 0 {
					if hasToolCallsMsg {
						transcript = append(transcript, toolCallsMsg)
					}
					if len(visibleToolMessages) > 0 {
						transcript = append(transcript, visibleToolMessages...)
					}
					continue
				}
				content = ""
			}
			if hasToolCallsMsg && utils.ToolCallExplanationDuplicatesContent(content, msg.ToolCalls) {
				content = ""
			}

			chatMsg := sessionChatMessage{
				Role:        "assistant",
				Content:     content,
				ModelName:   msg.ModelName,
				CreatedAt:   msg.CreatedAt,
				Media:       append([]string(nil), msg.Media...),
				Attachments: attachments,
			}
			if !sessionChatMessageVisible(chatMsg) {
				if hasToolCallsMsg {
					transcript = append(transcript, toolCallsMsg)
				}
				if len(visibleToolMessages) > 0 {
					transcript = append(transcript, visibleToolMessages...)
				}
				continue
			}

			transcript = append(transcript, chatMsg)
			if hasToolCallsMsg {
				transcript = append(transcript, toolCallsMsg)
			}
			if len(visibleToolMessages) > 0 {
				transcript = append(transcript, visibleToolMessages...)
			}
		}
	}

	return filterSessionChatMessages(transcript)
}

func filterSessionChatMessages(messages []sessionChatMessage) []sessionChatMessage {
	filtered := messages[:0]
	for _, msg := range messages {
		if msg.Role != "user" && msg.Role != "assistant" {
			continue
		}
		filtered = append(filtered, msg)
	}
	return filtered
}

func sessionAttachments(msg providers.Message) []sessionChatAttachment {
	if len(msg.Attachments) == 0 {
		return nil
	}

	attachments := make([]sessionChatAttachment, 0, len(msg.Attachments))
	for _, attachment := range msg.Attachments {
		urlValue, ok := sessionAttachmentURL(attachment)
		if !ok {
			continue
		}
		attachmentType := strings.TrimSpace(attachment.Type)
		if attachmentType == "" {
			attachmentType = sessionAttachmentType(attachment)
		}
		attachments = append(attachments, sessionChatAttachment{
			Type:        attachmentType,
			URL:         urlValue,
			Filename:    strings.TrimSpace(attachment.Filename),
			ContentType: strings.TrimSpace(attachment.ContentType),
		})
	}

	if len(attachments) == 0 {
		return nil
	}
	return attachments
}

func sessionAttachmentURL(attachment providers.Attachment) (string, bool) {
	if rawURL := strings.TrimSpace(attachment.URL); rawURL != "" {
		return rawURL, true
	}

	ref := strings.TrimSpace(attachment.Ref)
	if ref == "" {
		return "", false
	}
	if strings.HasPrefix(ref, "media://") {
		// Persisted session history must only expose durable attachment locations.
		// media:// refs depend on the live in-memory MediaStore and may stop
		// resolving after a restart or cleanup, so omit them from reopened history.
		return "", false
	}
	return ref, true
}

func sessionAttachmentType(attachment providers.Attachment) string {
	contentType := strings.ToLower(strings.TrimSpace(attachment.ContentType))
	filename := strings.ToLower(strings.TrimSpace(attachment.Filename))
	rawRef := strings.ToLower(strings.TrimSpace(attachment.Ref))
	rawURL := strings.ToLower(strings.TrimSpace(attachment.URL))

	switch {
	case strings.HasPrefix(contentType, "image/"),
		strings.HasPrefix(rawRef, "data:image/"),
		strings.HasPrefix(rawURL, "data:image/"):
		return "image"
	case strings.HasPrefix(contentType, "audio/"):
		return "audio"
	case strings.HasPrefix(contentType, "video/"):
		return "video"
	}

	switch ext := filepath.Ext(filename); ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg":
		return "image"
	case ".mp3", ".wav", ".ogg", ".m4a", ".flac", ".aac", ".wma", ".opus":
		return "audio"
	case ".mp4", ".avi", ".mov", ".webm", ".mkv":
		return "video"
	default:
		return "file"
	}
}

func assistantMessageInternalOnly(msg providers.Message) bool {
	return strings.TrimSpace(msg.Content) == handledToolResponseSummaryText
}

func assistantThoughtMessage(msg providers.Message) (sessionChatMessage, bool) {
	reasoning := strings.TrimSpace(msg.ReasoningContent)
	if reasoning == "" {
		return sessionChatMessage{}, false
	}
	if reasoning == strings.TrimSpace(msg.Content) {
		return sessionChatMessage{}, false
	}
	return sessionChatMessage{
		Role:      "assistant",
		Content:   reasoning,
		Kind:      "thought",
		ModelName: msg.ModelName,
		CreatedAt: msg.CreatedAt,
	}, true
}

func assistantToolCallsMessage(
	toolCalls []providers.ToolCall,
	modelName string,
	toolFeedbackMaxArgsLength int,
	createdAt *time.Time,
) (sessionChatMessage, bool) {
	if len(toolCalls) == 0 {
		return sessionChatMessage{}, false
	}
	if toolFeedbackMaxArgsLength <= 0 {
		toolFeedbackMaxArgsLength = defaultToolFeedbackMaxArgsLength()
	}

	visibleToolCalls := utils.BuildVisibleToolCalls(toolCalls, toolFeedbackMaxArgsLength)
	if len(visibleToolCalls) == 0 {
		return sessionChatMessage{}, false
	}

	return sessionChatMessage{
		Role:      "assistant",
		Kind:      "tool_calls",
		ModelName: modelName,
		CreatedAt: createdAt,
		ToolCalls: visibleToolCalls,
	}, true
}

func visibleAssistantToolArgsPreview(
	tc providers.ToolCall,
	toolFeedbackMaxArgsLength int,
) string {
	return utils.VisibleToolCallArgumentsPreview(tc, toolFeedbackMaxArgsLength)
}

func visibleAssistantToolMessages(
	toolCalls []providers.ToolCall,
	modelName string,
	createdAt *time.Time,
) []sessionChatMessage {
	if len(toolCalls) == 0 {
		return nil
	}

	messages := make([]sessionChatMessage, 0, len(toolCalls))
	for _, tc := range toolCalls {
		name, argsJSON := utils.VisibleToolCallNameAndArguments(tc)
		if name != "message" {
			continue
		}
		content, ok := parseMessageToolContent(argsJSON)
		if !ok {
			continue
		}
		messages = append(messages, sessionChatMessage{
			Role:      "assistant",
			Content:   content,
			ModelName: modelName,
			CreatedAt: createdAt,
		})
	}

	return messages
}

func parseMessageToolContent(argsJSON string) (string, bool) {
	var args struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", false
	}
	if strings.TrimSpace(args.Content) == "" {
		return "", false
	}
	return args.Content, true
}

// sessionsDir resolves the path to the gateway's session storage directory.
// It reads the workspace from config, falling back to ~/.picoclaw/workspace.
func (h *Handler) sessionsDir() (string, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return "", err
	}

	return resolveSessionsDir(cfg.Agents.Defaults.Workspace), nil
}

func (h *Handler) sessionRuntimeSettings() (string, int, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return "", 0, err
	}

	return resolveSessionsDir(cfg.Agents.Defaults.Workspace), cfg.Agents.Defaults.GetToolFeedbackMaxArgsLength(), nil
}

func resolveSessionsDir(workspace string) string {
	if workspace == "" {
		home, _ := os.UserHomeDir()
		workspace = filepath.Join(home, ".picoclaw", "workspace")
	}

	// Expand ~ prefix
	if len(workspace) > 0 && workspace[0] == '~' {
		home, _ := os.UserHomeDir()
		if len(workspace) > 1 && workspace[1] == '/' {
			workspace = home + workspace[1:]
		} else {
			workspace = home
		}
	}

	return filepath.Join(workspace, "sessions")
}

// handleListSessions returns a list of Pico session summaries.
//
//	GET /api/sessions
func (h *Handler) handleListSessions(w http.ResponseWriter, r *http.Request) {
	dir, toolFeedbackMaxArgsLength, err := h.sessionRuntimeSettings()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}

	if _, err := os.ReadDir(dir); err != nil {
		// Directory doesn't exist yet = no sessions
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]sessionListItem{})
		return
	}

	items := []sessionListItem{}
	seen := make(map[string]struct{})

	if refs, findErr := h.findPicoJSONLSessions(dir); findErr == nil {
		for _, ref := range refs {
			sess, loadErr := h.readJSONLSession(dir, ref.Key)
			if loadErr != nil || isEmptySession(sess) {
				continue
			}
			seen[ref.ID] = struct{}{}
			metaPath := filepath.Join(dir, sanitizeSessionKey(ref.Key)+".meta.json")
			meta, _ := h.readSessionMeta(metaPath, ref.Key)
			items = append(items, buildSessionListItem(ref.ID, sess, meta, toolFeedbackMaxArgsLength))
		}
	}

	if legacyRefs, findErr := h.findLegacyPicoSessions(dir); findErr == nil {
		for _, ref := range legacyRefs {
			if _, exists := seen[ref.ID]; exists {
				continue
			}
			sess, loadErr := h.readLegacySession(ref.Path)
			if loadErr != nil || isEmptySession(sess) {
				continue
			}
			seen[ref.ID] = struct{}{}
			items = append(items, buildSessionListItem(ref.ID, sess, memory.SessionMeta{}, toolFeedbackMaxArgsLength))
		}
	}

	// Sort by favorited descending (favorited first), then by updated descending (most recent first)
	sort.Slice(items, func(i, j int) bool {
		if items[i].IsFavorited != items[j].IsFavorited {
			return items[i].IsFavorited // Favorited items come first
		}
		return items[i].Updated > items[j].Updated
	})

	// Pagination parameters
	offsetStr := r.URL.Query().Get("offset")
	limitStr := r.URL.Query().Get("limit")

	offset := 0
	limit := 20 // Default limit

	if val, err := strconv.Atoi(offsetStr); err == nil && val >= 0 {
		offset = val
	}
	if val, err := strconv.Atoi(limitStr); err == nil && val > 0 {
		limit = val
	}

	totalItems := len(items)

	end := offset + limit
	if offset >= totalItems {
		items = []sessionListItem{} // Out of bounds, return empty
	} else {
		if end > totalItems {
			end = totalItems
		}
		items = items[offset:end]
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

// handleGetSession returns the full message history for a specific session.
//
//	GET /api/sessions/{id}
func (h *Handler) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	dir, toolFeedbackMaxArgsLength, err := h.sessionRuntimeSettings()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}

	ref, refErr := h.findPicoJSONLSession(dir, sessionID)
	var sess sessionFile
	err = refErr
	if refErr == nil {
		sess, err = h.readJSONLSession(dir, ref.Key)
		if err == nil && isEmptySession(sess) && strings.TrimSpace(sess.AgentPreset) == "" {
			metaPath := filepath.Join(dir, sanitizeSessionKey(ref.Key)+".meta.json")
			if _, statErr := os.Stat(metaPath); os.IsNotExist(statErr) {
				err = os.ErrNotExist
			}
		}
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if legacyRef, legacyErr := h.findLegacyPicoSession(dir, sessionID); legacyErr == nil {
				sess, err = h.readLegacySession(legacyRef.Path)
			}
		}
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.Error(w, "session not found", http.StatusNotFound)
			} else {
				http.Error(w, "failed to parse session", http.StatusInternalServerError)
			}
			return
		}
	}

	response := h.buildSessionDetailResponse(sessionID, sess, toolFeedbackMaxArgsLength)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *Handler) buildSessionDetailResponse(
	sessionID string,
	sess sessionFile,
	toolFeedbackMaxArgsLength int,
) sessionDetailResponse {
	for i := range sess.Messages {
		if sess.Messages[i].CreatedAt == nil {
			sess.Messages[i].CreatedAt = &sess.Updated
		}
	}
	messages := detailSessionMessages(sess.Messages, toolFeedbackMaxArgsLength)
	cfg, configErr := config.LoadConfig(h.configPath)
	effectiveModel := ""
	if configErr == nil {
		route, _ := picoRouteAllocation(cfg, sessionID)
		effectiveModel, _, _ = effectiveModelForAgentPreset(cfg, route.AgentID, sess.AgentPreset)
	}

	// archivedCount is the number of leading transcript entries that come from
	// the archive (compaction-dropped history). The frontend uses it to render a
	// "view-only compressed history" divider. detailSessionMessages is
	// concatenation-preserving, so the archived prefix maps to a stable count.
	archivedRaw := sess.Messages[:sess.ArchivedRawCount]
	archivedCount := len(detailSessionMessages(archivedRaw, toolFeedbackMaxArgsLength))

	return sessionDetailResponse{
		ID:             sessionID,
		Messages:       messages,
		Summary:        sess.Summary,
		AgentPreset:    agentPresetResponseName(sess.AgentPreset),
		EffectiveModel: effectiveModel,
		ArchivedCount:  archivedCount,
		Created:        sess.Created.Format(time.RFC3339),
		Updated:        sess.Updated.Format(time.RFC3339),
	}
}

// handleDeleteSession deletes a specific session.
//
//	DELETE /api/sessions/{id}
func (h *Handler) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	dir, err := h.sessionsDir()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}

	removed := false
	if ref, err := h.findPicoJSONLSession(dir, sessionID); err == nil {
		base := filepath.Join(dir, sanitizeSessionKey(ref.Key))
		for _, path := range []string{base + ".jsonl", base + ".meta.json", base + ".archive.jsonl"} {
			if err := os.Remove(path); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				http.Error(w, "failed to delete session", http.StatusInternalServerError)
				return
			}
			removed = true
		}
	}

	if legacyRef, err := h.findLegacyPicoSession(dir, sessionID); err == nil {
		if err := os.Remove(legacyRef.Path); err != nil {
			if !os.IsNotExist(err) {
				http.Error(w, "failed to delete session", http.StatusInternalServerError)
				return
			}
		} else {
			removed = true
		}
	}

	if !removed {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleFavoriteSession marks a session as favorited.
//
//	POST /api/sessions/{id}/favorite
func (h *Handler) handleFavoriteSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	if err := h.toggleSessionFavorite(sessionID, true); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "session not found", http.StatusNotFound)
		} else {
			http.Error(w, "failed to favorite session", http.StatusInternalServerError)
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleUnfavoriteSession removes the favorite mark from a session.
//
//	DELETE /api/sessions/{id}/favorite
func (h *Handler) handleUnfavoriteSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	if err := h.toggleSessionFavorite(sessionID, false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "session not found", http.StatusNotFound)
		} else {
			http.Error(w, "failed to unfavorite session", http.StatusInternalServerError)
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// renameSessionRequest represents the request to rename a session.
type renameSessionRequest struct {
	Title string `json:"title"`
}

// handleRenameSession renames a session by setting a custom title.
//
//	POST /api/sessions/{id}/rename
//	Request body: {"title": "new title"}
func (h *Handler) handleRenameSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	var req renameSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	req.Title = strings.TrimSpace(req.Title)

	dir, err := h.sessionsDir()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}

	ref, refErr := h.findPicoJSONLSession(dir, sessionID)
	if refErr != nil {
		if errors.Is(refErr, os.ErrNotExist) {
			http.Error(w, "session not found", http.StatusNotFound)
		} else {
			http.Error(w, "failed to find session", http.StatusInternalServerError)
		}
		return
	}

	base := filepath.Join(dir, sanitizeSessionKey(ref.Key))
	metaPath := base + ".meta.json"

	meta, err := h.readSessionMeta(metaPath, ref.Key)
	if err != nil {
		meta = memory.SessionMeta{Key: ref.Key}
	}

	meta.Title = req.Title

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		http.Error(w, "failed to encode session meta", http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(metaPath, data, 0o644); err != nil {
		http.Error(w, "failed to write session meta", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"id":    sessionID,
		"title": req.Title,
	})
}

// toggleSessionFavorite updates the favorite status of a session.
func (h *Handler) toggleSessionFavorite(sessionID string, favorited bool) error {
	dir, err := h.sessionsDir()
	if err != nil {
		return err
	}

	// Try to find JSONL session first
	ref, refErr := h.findPicoJSONLSession(dir, sessionID)
	if refErr == nil {
		base := filepath.Join(dir, sanitizeSessionKey(ref.Key))
		metaPath := base + ".meta.json"

		meta, err := h.readSessionMeta(metaPath, ref.Key)
		if err != nil {
			meta = memory.SessionMeta{Key: ref.Key}
		}

		meta.Favorited = favorited

		data, err := json.MarshalIndent(meta, "", "  ")
		if err != nil {
			return err
		}

		if err := os.WriteFile(metaPath, data, 0o644); err != nil {
			return err
		}

		return nil
	}

	// Try legacy session (read-only, can't favorite)
	if _, err := h.findLegacyPicoSession(dir, sessionID); err == nil {
		return errors.New("cannot favorite legacy session")
	}

	return os.ErrNotExist
}

// forkSessionRequest represents the request to fork a session at a specific
// transcript index. The transcript index refers to the position in the
// detail-session-messages output (which includes thoughts), matching what
// GET /api/sessions/{id} returns. The backend maps this back to the
// corresponding raw persisted message boundary.
type forkSessionRequest struct {
	NewSessionID    string `json:"new_session_id"`
	TranscriptIndex int    `json:"transcript_index"`
}

// forkSessionResponse represents the response from forking a session.
type forkSessionResponse struct {
	SourceSessionID string `json:"source_session_id"`
	NewSessionID    string `json:"new_session_id"`
}

// rawMessageIndexForTranscriptIndex maps a transcript index (position in the
// detail-session-messages output) back to the corresponding raw persisted
// message index. Returns -1 when the transcript index is out of range.
//
// The mapping mirrors sessionTranscriptMessages(..., includeThoughts=true)
// so it stays consistent with what GET /api/sessions/{id} returns.
func rawMessageIndexForTranscriptIndex(messages []providers.Message, transcriptIndex int, toolFeedbackMaxArgsLength int) int {
	if transcriptIndex < 0 || len(messages) == 0 {
		return -1
	}
	if toolFeedbackMaxArgsLength <= 0 {
		toolFeedbackMaxArgsLength = defaultToolFeedbackMaxArgsLength()
	}

	transcriptPos := 0
	for rawIdx, msg := range messages {
		attachments := sessionAttachments(msg)

		switch msg.Role {
		case "tool":
			// tool messages never appear in the transcript
			continue

		case "user":
			chatMsg := sessionChatMessage{
				Role:        "user",
				Content:     msg.Content,
				ModelName:   msg.ModelName,
				Media:       append([]string(nil), msg.Media...),
				Attachments: attachments,
			}
			if sessionChatMessageVisible(chatMsg) {
				if transcriptPos == transcriptIndex {
					return rawIdx + 1 // include this message in the fork
				}
				transcriptPos++
			}

		case "assistant":
			if messageutil.IsTransientAssistantThoughtMessage(msg) {
				continue
			}

			// Thought entry (includeThoughts=true for detail view)
			if _, ok := assistantThoughtMessage(msg); ok {
				if transcriptPos == transcriptIndex {
					return rawIdx + 1
				}
				transcriptPos++
			}

			_, hasToolCallsMsg := assistantToolCallsMessage(
				msg.ToolCalls,
				msg.ModelName,
				toolFeedbackMaxArgsLength,
				msg.CreatedAt,
			)
			visibleToolMessages := visibleAssistantToolMessages(msg.ToolCalls, msg.ModelName, msg.CreatedAt)

			content := msg.Content
			if assistantMessageInternalOnly(msg) {
				if len(attachments) == 0 {
					if hasToolCallsMsg {
						if transcriptPos == transcriptIndex {
							return rawIdx + 1
						}
						transcriptPos++
					}
					for range visibleToolMessages {
						if transcriptPos == transcriptIndex {
							return rawIdx + 1
						}
						transcriptPos++
					}
					continue
				}
				content = ""
			}
			if hasToolCallsMsg && utils.ToolCallExplanationDuplicatesContent(content, msg.ToolCalls) {
				content = ""
			}

			chatMsg := sessionChatMessage{
				Role:        "assistant",
				Content:     content,
				ModelName:   msg.ModelName,
				Media:       append([]string(nil), msg.Media...),
				Attachments: attachments,
			}
			if !sessionChatMessageVisible(chatMsg) {
				if hasToolCallsMsg {
					if transcriptPos == transcriptIndex {
						return rawIdx + 1
					}
					transcriptPos++
				}
				for range visibleToolMessages {
					if transcriptPos == transcriptIndex {
						return rawIdx + 1
					}
					transcriptPos++
				}
				continue
			}

			if transcriptPos == transcriptIndex {
				return rawIdx + 1
			}
			transcriptPos++

			if hasToolCallsMsg {
				if transcriptPos == transcriptIndex {
					return rawIdx + 1
				}
				transcriptPos++
			}
			for range visibleToolMessages {
				if transcriptPos == transcriptIndex {
					return rawIdx + 1
				}
				transcriptPos++
			}
		}
	}

	// transcriptIndex beyond the last transcript entry → include all messages
	if transcriptPos <= transcriptIndex {
		return len(messages)
	}
	return -1
}

func forkableTranscriptMessage(msg sessionChatMessage) bool {
	return msg.Role == "user" ||
		(msg.Role == "assistant" && (msg.Kind == "" || msg.Kind == "normal"))
}

// safeRawBoundaryForTranscriptIndex returns the raw persisted boundary after a
// visible conversation message. When the source assistant record contains tool
// calls, the boundary also includes its contiguous matching tool results and a
// hidden handled-response marker. This keeps both fork and delete operations
// from producing orphaned tool protocol records.
func safeRawBoundaryForTranscriptIndex(
	messages []providers.Message,
	transcriptIndex int,
	toolFeedbackMaxArgsLength int,
) int {
	rawCount := rawMessageIndexForTranscriptIndex(
		messages,
		transcriptIndex,
		toolFeedbackMaxArgsLength,
	)
	if rawCount <= 0 || rawCount > len(messages) {
		return -1
	}

	source := messages[rawCount-1]
	if source.Role != "assistant" || len(source.ToolCalls) == 0 {
		return rawCount
	}

	expectedToolResults := make(map[string]struct{}, len(source.ToolCalls))
	for _, toolCall := range source.ToolCalls {
		if toolCall.ID != "" {
			expectedToolResults[toolCall.ID] = struct{}{}
		}
	}

	boundary := rawCount
	for boundary < len(messages) && messages[boundary].Role == "tool" {
		toolCallID := messages[boundary].ToolCallID
		if _, ok := expectedToolResults[toolCallID]; !ok {
			break
		}
		boundary++
	}

	if boundary < len(messages) {
		marker := messages[boundary]
		if assistantMessageInternalOnly(marker) &&
			len(sessionAttachments(marker)) == 0 &&
			len(marker.Media) == 0 {
			boundary++
		}
	}

	return boundary
}

// rawMessageRangeForTranscriptIndex resolves the complete persisted message
// series ending at transcriptIndex. The start is exclusive of the previous
// forkable conversation boundary and the end includes the selected message.
func rawMessageRangeForTranscriptIndex(
	messages []providers.Message,
	transcriptIndex int,
	toolFeedbackMaxArgsLength int,
) (int, int, bool) {
	transcript := detailSessionMessages(messages, toolFeedbackMaxArgsLength)
	if transcriptIndex < 0 || transcriptIndex >= len(transcript) ||
		!forkableTranscriptMessage(transcript[transcriptIndex]) {
		return 0, 0, false
	}

	end := safeRawBoundaryForTranscriptIndex(
		messages,
		transcriptIndex,
		toolFeedbackMaxArgsLength,
	)
	if end <= 0 {
		return 0, 0, false
	}

	start := 0
	for index := transcriptIndex - 1; index >= 0; index-- {
		if !forkableTranscriptMessage(transcript[index]) {
			continue
		}
		boundary := safeRawBoundaryForTranscriptIndex(
			messages,
			index,
			toolFeedbackMaxArgsLength,
		)
		// Multiple visible entries can be projections of the same raw assistant
		// record (for example content plus a visible message-tool output). Treat
		// those projections as one indivisible persisted series.
		if boundary > 0 && boundary < end {
			start = boundary
			break
		}
	}

	if start >= end || end > len(messages) {
		return 0, 0, false
	}
	return start, end, true
}

func rawRangeOverlap(start, end, rangeStart, rangeEnd int) int {
	overlapStart := max(start, rangeStart)
	overlapEnd := min(end, rangeEnd)
	if overlapEnd <= overlapStart {
		return 0
	}
	return overlapEnd - overlapStart
}

type contextSummaryProviderFactory func(*config.Config) (providers.LLMProvider, string, error)

func defaultContextSummaryProviderFactory(
	cfg *config.Config,
) (providers.LLMProvider, string, error) {
	return providers.CreateProvider(cfg)
}

// rebuildArchivedSummary deliberately passes an empty existing summary: the
// stored summary may describe the message that was just deleted. The remaining
// archive is summarized through the exact same batching, merge, retry, and
// fallback implementation used by normal legacy context compression.
func (h *Handler) rebuildArchivedSummary(
	ctx context.Context,
	messages []providers.Message,
) (string, error) {
	if len(messages) == 0 {
		return "", nil
	}

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return "", err
	}
	maxTokens := cfg.Agents.Defaults.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}
	contextWindow := cfg.Agents.Defaults.ContextWindow
	if contextWindow == 0 {
		contextWindow = maxTokens * 4
	}

	factory := h.contextSummaryProvider
	if factory == nil {
		factory = defaultContextSummaryProviderFactory
	}
	provider, modelID, providerErr := factory(cfg)
	var complete agentpkg.ContextSummaryCompletion
	if providerErr != nil {
		logger.WarnCF("launcher", "Failed to create provider for archive summary rebuild; using fallback", map[string]any{
			"error": providerErr.Error(),
		})
	} else if provider == nil {
		logger.WarnCF("launcher", "Archive summary provider is nil; using fallback", nil)
	} else {
		if stateful, ok := provider.(providers.StatefulProvider); ok {
			defer stateful.Close()
		}
		complete = func(ctx context.Context, prompt string) (string, error) {
			response, chatErr := provider.Chat(
				ctx,
				[]providers.Message{{Role: "user", Content: prompt}},
				nil,
				modelID,
				map[string]any{
					"max_tokens":       maxTokens,
					"temperature":      agentpkg.ContextSummaryTemperature(),
					"prompt_cache_key": routing.DefaultAgentID,
				},
			)
			if response == nil {
				return "", chatErr
			}
			return response.Content, chatErr
		}
	}

	summaryCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	result := agentpkg.BuildContextSummary(
		summaryCtx,
		messages,
		"",
		contextWindow,
		complete,
	)
	return result.Summary, nil
}

// handleDeleteMessageSeries removes the complete raw message series ending at
// a visible user or normal assistant transcript entry.
//
//	DELETE /api/sessions/{id}/message-series?transcript_index=3
func (h *Handler) handleDeleteMessageSeries(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	if sessionID == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	transcriptIndex, err := strconv.Atoi(r.URL.Query().Get("transcript_index"))
	if err != nil || transcriptIndex < 0 {
		http.Error(w, "invalid transcript_index", http.StatusBadRequest)
		return
	}

	dir, toolFeedbackMaxArgsLength, err := h.sessionRuntimeSettings()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}

	if ref, refErr := h.findPicoJSONLSession(dir, sessionID); refErr == nil {
		sess, readErr := h.readJSONLSession(dir, ref.Key)
		if readErr != nil {
			http.Error(w, "failed to read session", http.StatusInternalServerError)
			return
		}

		start, end, ok := rawMessageRangeForTranscriptIndex(
			sess.Messages,
			transcriptIndex,
			toolFeedbackMaxArgsLength,
		)
		if !ok {
			http.Error(w, "transcript_index is not a deletable message boundary", http.StatusBadRequest)
			return
		}

		remaining := make([]providers.Message, 0, len(sess.Messages)-(end-start))
		remaining = append(remaining, sess.Messages[:start]...)
		remaining = append(remaining, sess.Messages[end:]...)

		archivedDeleted := rawRangeOverlap(start, end, 0, sess.ArchivedRawCount)
		activeDeleted := rawRangeOverlap(start, end, sess.ArchivedRawCount, len(sess.Messages))
		newArchivedCount := sess.ArchivedRawCount - archivedDeleted
		archived := append([]providers.Message(nil), remaining[:newArchivedCount]...)
		active := append([]providers.Message(nil), remaining[newArchivedCount:]...)
		rebuiltSummary := ""
		if archivedDeleted > 0 {
			rebuiltSummary, err = h.rebuildArchivedSummary(r.Context(), archived)
			if err != nil {
				http.Error(w, "failed to rebuild session summary", http.StatusInternalServerError)
				return
			}
		}

		store, storeErr := memory.NewJSONLStore(dir)
		if storeErr != nil {
			http.Error(w, "failed to open session store", http.StatusInternalServerError)
			return
		}
		if activeDeleted > 0 {
			if err := store.SetHistory(r.Context(), ref.Key, active); err != nil {
				http.Error(w, "failed to update session history", http.StatusInternalServerError)
				return
			}
		}
		if archivedDeleted > 0 {
			if err := store.ReplaceArchivedMessages(r.Context(), ref.Key, archived); err != nil {
				http.Error(w, "failed to update archived history", http.StatusInternalServerError)
				return
			}
			if err := store.SetSummary(r.Context(), ref.Key, rebuiltSummary); err != nil {
				http.Error(w, "failed to rebuild session summary", http.StatusInternalServerError)
				return
			}
		}

		if err := h.reconcileEditedSessionContext(r.Context(), dir, ref.Key, active); err != nil {
			http.Error(w, "failed to reconcile session context", http.StatusInternalServerError)
			return
		}

		updated, readErr := h.readJSONLSession(dir, ref.Key)
		if readErr != nil {
			http.Error(w, "failed to reload session", http.StatusInternalServerError)
			return
		}
		response := h.buildSessionDetailResponse(sessionID, updated, toolFeedbackMaxArgsLength)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	} else if !errors.Is(refErr, os.ErrNotExist) {
		http.Error(w, "failed to find session", http.StatusInternalServerError)
		return
	}

	legacyRef, legacyErr := h.findLegacyPicoSession(dir, sessionID)
	if legacyErr != nil {
		if errors.Is(legacyErr, os.ErrNotExist) {
			http.Error(w, "session not found", http.StatusNotFound)
		} else {
			http.Error(w, "failed to find session", http.StatusInternalServerError)
		}
		return
	}

	sess, err := h.readLegacySession(legacyRef.Path)
	if err != nil {
		http.Error(w, "failed to read session", http.StatusInternalServerError)
		return
	}
	start, end, ok := rawMessageRangeForTranscriptIndex(
		sess.Messages,
		transcriptIndex,
		toolFeedbackMaxArgsLength,
	)
	if !ok {
		http.Error(w, "transcript_index is not a deletable message boundary", http.StatusBadRequest)
		return
	}

	remaining := make([]providers.Message, 0, len(sess.Messages)-(end-start))
	remaining = append(remaining, sess.Messages[:start]...)
	remaining = append(remaining, sess.Messages[end:]...)
	sess.Messages = remaining
	if strings.TrimSpace(sess.Summary) != "" {
		sess.Summary, err = h.rebuildArchivedSummary(r.Context(), remaining)
		if err != nil {
			http.Error(w, "failed to rebuild session summary", http.StatusInternalServerError)
			return
		}
	}
	sess.Updated = time.Now()
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		http.Error(w, "failed to encode session", http.StatusInternalServerError)
		return
	}
	if err := fileutil.WriteFileAtomic(legacyRef.Path, data, 0o600); err != nil {
		http.Error(w, "failed to update session history", http.StatusInternalServerError)
		return
	}
	if err := h.reconcileEditedSessionContext(r.Context(), dir, sess.Key, remaining); err != nil {
		http.Error(w, "failed to reconcile session context", http.StatusInternalServerError)
		return
	}

	response := h.buildSessionDetailResponse(sessionID, sess, toolFeedbackMaxArgsLength)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleForkSession creates a new session with messages from an existing session up to a specific transcript index.
//
//	POST /api/sessions/{id}/fork
//	Request body: {"new_session_id": "uuid", "transcript_index": 3}
func (h *Handler) handleForkSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if sessionID == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	var req forkSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	req.NewSessionID = strings.TrimSpace(req.NewSessionID)
	if req.NewSessionID == "" {
		http.Error(w, "missing new_session_id", http.StatusBadRequest)
		return
	}
	if req.NewSessionID == sessionID {
		http.Error(w, "new_session_id must differ from source session id", http.StatusBadRequest)
		return
	}

	if req.TranscriptIndex < 0 {
		http.Error(w, "invalid transcript_index", http.StatusBadRequest)
		return
	}

	dir, toolFeedbackMaxArgsLength, err := h.sessionRuntimeSettings()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}

	// Build the same routed scope the gateway will use when the first message
	// is sent in the forked Pico session.
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, "failed to load config", http.StatusInternalServerError)
		return
	}
	_, allocation := picoRouteAllocation(cfg, req.NewSessionID)
	scope := allocation.Scope
	newSessionKey := allocation.SessionKey

	// Reject if the new session already exists (check both opaque and legacy).
	if _, err := h.findPicoJSONLSession(dir, req.NewSessionID); err == nil {
		http.Error(w, "new session already exists", http.StatusConflict)
		return
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		http.Error(w, "failed to check new session", http.StatusInternalServerError)
		return
	}
	if _, err := h.findLegacyPicoSession(dir, req.NewSessionID); err == nil {
		http.Error(w, "new session already exists", http.StatusConflict)
		return
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		http.Error(w, "failed to check new session", http.StatusInternalServerError)
		return
	}

	// Find the source session.
	ref, refErr := h.findPicoJSONLSession(dir, sessionID)
	var sess sessionFile
	err = refErr
	if refErr == nil {
		sess, err = h.readJSONLSession(dir, ref.Key)
	}
	if err == nil && isEmptySession(sess) {
		err = os.ErrNotExist
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Try legacy session
			if legacyRef, legacyErr := h.findLegacyPicoSession(dir, sessionID); legacyErr == nil {
				sess, err = h.readLegacySession(legacyRef.Path)
			}
			if err == nil && isEmptySession(sess) {
				err = os.ErrNotExist
			}
		}
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.Error(w, "source session not found", http.StatusNotFound)
			} else {
				http.Error(w, "failed to read source session", http.StatusInternalServerError)
			}
			return
		}
	}

	// Map the transcript index back to the raw persisted message boundary.
	// The transcript index refers to the position in the detail-session-messages
	// output (which includes thoughts), matching what GET /api/sessions/{id}
	// returns to the frontend.
	fullTranscript := detailSessionMessages(sess.Messages, toolFeedbackMaxArgsLength)
	if req.TranscriptIndex >= len(fullTranscript) ||
		!forkableTranscriptMessage(fullTranscript[req.TranscriptIndex]) {
		http.Error(w, "cannot fork at a thought or tool-call message", http.StatusBadRequest)
		return
	}
	rawCount := safeRawBoundaryForTranscriptIndex(
		sess.Messages,
		req.TranscriptIndex,
		toolFeedbackMaxArgsLength,
	)
	if rawCount <= 0 {
		http.Error(w, "transcript_index out of range", http.StatusBadRequest)
		return
	}

	forkedMessages := sess.Messages[:rawCount]

	// Write the new session files using the opaque key.
	base := filepath.Join(dir, sanitizeSessionKey(newSessionKey))
	jsonlPath := base + ".jsonl"
	metaPath := base + ".meta.json"

	// Write JSONL file with forked messages.
	jsonlFile, err := os.Create(jsonlPath)
	if err != nil {
		http.Error(w, "failed to create new session file", http.StatusInternalServerError)
		return
	}
	defer jsonlFile.Close()

	encoder := json.NewEncoder(jsonlFile)
	for _, msg := range forkedMessages {
		if err := encoder.Encode(msg); err != nil {
			http.Error(w, "failed to write messages to new session", http.StatusInternalServerError)
			return
		}
	}

	// Write metadata file with legacy alias for compatibility.
	now := time.Now()

	scopeData, err := json.Marshal(scope)
	if err != nil {
		http.Error(w, "failed to marshal session scope", http.StatusInternalServerError)
		return
	}

	meta := memory.SessionMeta{
		Key:         newSessionKey,
		AgentPreset: sess.AgentPreset,
		Count:       len(forkedMessages),
		CreatedAt:   now,
		UpdatedAt:   now,
		Scope:       scopeData,
		Aliases:     append([]string(nil), allocation.SessionAliases...),
	}

	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		http.Error(w, "failed to marshal session metadata", http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(metaPath, metaData, 0o644); err != nil {
		http.Error(w, "failed to write session metadata", http.StatusInternalServerError)
		return
	}

	// Return success response.
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(forkSessionResponse{
		SourceSessionID: sessionID,
		NewSessionID:    req.NewSessionID,
	})
}
