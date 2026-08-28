package agent

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// TestPauseActiveTurnForSession_GracefulInterrupt verifies that pausing an
// active turn sets the graceful-interrupt flag (not hard abort) and keeps the
// session history intact (no rollback).
func TestPauseActiveTurnForSession_GracefulInterrupt(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	sess := &ephemeralSessionStore{
		history: []providers.Message{
			{Role: "user", Content: "initial message 1"},
			{Role: "assistant", Content: "initial response 1"},
		},
	}

	rootTS := &turnState{
		ctx:                  context.Background(),
		turnID:               "test-pause-session",
		sessionKey:           "test-pause-session",
		depth:                0,
		session:              sess,
		initialHistoryLength: 2,
		pendingResults:       make(chan *tools.ToolResult, 16),
		concurrencySem:       make(chan struct{}, 5),
		userMessage:          "sync the long running job",
	}
	al.activeTurnStates.Store("test-pause-session", rootTS)
	defer al.activeTurnStates.Delete("test-pause-session")

	// Simulate messages added during the turn.
	sess.AddMessage("", "user", "new user message")
	sess.AddMessage("", "assistant", "new assistant response")

	if len(sess.GetHistory("")) != 4 {
		t.Fatalf("expected 4 messages before pause, got %d", len(sess.GetHistory("")))
	}

	result, err := al.pauseActiveTurnForSession("test-pause-session")
	if err != nil {
		t.Fatalf("pauseActiveTurnForSession error = %v", err)
	}
	if !result.Paused {
		t.Fatal("expected Paused=true")
	}
	if result.TaskName != "sync the long running job" {
		t.Errorf("TaskName=%q, want %q", result.TaskName, "sync the long running job")
	}

	// Graceful interrupt flag should be set, hard abort should not.
	graceful, _ := rootTS.gracefulInterruptRequested()
	if !graceful {
		t.Error("expected graceful interrupt to be requested")
	}
	if rootTS.hardAbortRequested() {
		t.Error("expected hard abort NOT to be requested on pause")
	}

	// History must be preserved (no rollback).
	finalHistory := sess.GetHistory("")
	if len(finalHistory) != 4 {
		t.Errorf("expected history to keep 4 messages after pause, got %d", len(finalHistory))
	}

	// A pause marker should be armed for Finalize to consume.
	if !al.takePauseMarker("test-pause-session") {
		t.Error("expected pause marker to be armed after pause")
	}
}

// TestPauseActiveTurnForSession_NoActiveTurn verifies pausing with no active
// turn returns Paused=false.
func TestPauseActiveTurnForSession_NoActiveTurn(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	result, err := al.pauseActiveTurnForSession("nonexistent-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Paused {
		t.Error("expected Paused=false when no active turn")
	}
}

// TestPauseActiveTurnForSession_PendingPlaceholder does not arm a marker or
// interrupt; the caller (tryHandlePauseCommand) arms a pending pause instead.
func TestPauseActiveTurnForSession_PendingPlaceholder(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	rootTS := &turnState{
		ctx:            context.Background(),
		turnID:         pendingTurnPrefix + "pending-1",
		sessionKey:     "pending-session",
		pendingResults: make(chan *tools.ToolResult, 4),
	}
	al.activeTurnStates.Store("pending-session", rootTS)
	defer al.activeTurnStates.Delete("pending-session")

	result, err := al.pauseActiveTurnForSession("pending-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Paused {
		t.Error("expected Paused=false for pending placeholder")
	}
	// No marker should be armed for a pending placeholder.
	if al.takePauseMarker("pending-session") {
		t.Error("expected no pause marker for pending placeholder")
	}
}

// TestPauseMarkerHelpers verifies the mark/take semantics of pause markers.
func TestPauseMarkerHelpers(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	if al.takePauseMarker("session-x") {
		t.Error("expected no marker initially")
	}

	al.markPauseMarker("session-x")
	if !al.takePauseMarker("session-x") {
		t.Error("expected marker to be present after marking")
	}
	// Second take should be false (consumed).
	if al.takePauseMarker("session-x") {
		t.Error("expected marker to be consumed after first take")
	}

	// Empty session key is a no-op.
	al.markPauseMarker("")
	if al.takePauseMarker("") {
		t.Error("expected no marker for empty session key")
	}
}

// TestPendingPauseHelpers verifies the mark/take semantics of pending pauses.
func TestPendingPauseHelpers(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	if al.takePendingPause("session-y") {
		t.Error("expected no pending pause initially")
	}

	al.markPendingPause("session-y")
	if !al.takePendingPause("session-y") {
		t.Error("expected pending pause to be present after marking")
	}
	if al.takePendingPause("session-y") {
		t.Error("expected pending pause to be consumed after first take")
	}

	// Empty session key is a no-op.
	al.markPendingPause("")
	if al.takePendingPause("") {
		t.Error("expected no pending pause for empty session key")
	}
}
