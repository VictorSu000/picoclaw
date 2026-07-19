package session

import "github.com/sipeed/picoclaw/pkg/providers"

// SessionStore defines the persistence operations used by the agent loop.
// Both SessionManager (legacy JSON backend) and JSONLBackend satisfy this
// interface, allowing the storage layer to be swapped without touching the
// agent loop code.
//
// Write methods (Add*, Set*, Truncate*) are fire-and-forget: they do not
// return errors. Implementations should log failures internally. This
// matches the original SessionManager contract that the agent loop relies on.
type SessionStore interface {
	// AddMessage appends a simple role/content message to the session.
	AddMessage(sessionKey, role, content string)
	// AddFullMessage appends a complete message including tool calls.
	AddFullMessage(sessionKey string, msg providers.Message)
	// GetHistory returns the full message history for the session.
	GetHistory(key string) []providers.Message
	// GetSummary returns the conversation summary, or "" if none.
	GetSummary(key string) string
	// SetSummary replaces the conversation summary.
	SetSummary(key, summary string)
	// SetHistory replaces the full message history.
	SetHistory(key string, history []providers.Message)
	// TruncateHistory keeps only the last keepLast messages.
	TruncateHistory(key string, keepLast int)
	// Save persists any pending state to durable storage.
	Save(key string) error
	// ListSessions returns all known session keys.
	ListSessions() []string
	// Close releases resources held by the store.
	Close() error
}

// ArchivingSessionStore is an optional interface a SessionStore may implement to
// preserve messages that context compaction drops from the active history.
// Archived messages are display-only (surfaced by the Web UI) and are never
// returned by GetHistory, so they never re-enter the LLM context.
//
// Callers should type-assert to this interface and skip archiving when the
// backing store does not implement it (e.g. the in-memory subturn store).
type ArchivingSessionStore interface {
	// ArchiveMessages appends dropped messages to the session's archive in
	// chronological order. Fire-and-forget: implementations log failures.
	ArchiveMessages(sessionKey string, msgs []providers.Message)
}

// TitleSessionStore is the optional persistence capability used for generated
// and user-supplied session titles.
type TitleSessionStore interface {
	// SetTitle overwrites the current title, including clearing it.
	SetTitle(sessionKey, title string) error
	// SetTitleIfEmpty stores title only when no title has been set yet.
	SetTitleIfEmpty(sessionKey, title string) (bool, error)
}

// AgentPresetSessionStore is the optional persistence capability used by the
// agent loop to remember the selected preset for one session.
type AgentPresetSessionStore interface {
	GetAgentPreset(sessionKey string) string
	SetAgentPreset(sessionKey, preset string) error
}

// AgentPresetOverrideSessionStore preserves whether a session explicitly
// selected a preset or should inherit its channel's default.
type AgentPresetOverrideSessionStore interface {
	GetAgentPresetOverride(sessionKey string) (preset string, override bool)
	SetAgentPresetOverride(sessionKey, preset string, override bool) error
}
