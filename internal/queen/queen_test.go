package queen

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/exedev/queen-bee/internal/config"
	"github.com/exedev/queen-bee/internal/state"
)

// TestCloseDoesNotOverwriteDoneStatus verifies that Close() does not overwrite
// a session status that is already 'done'.
func TestCloseDoesNotOverwriteDoneStatus(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "queen-close-done-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	hiveDir := filepath.Join(tempDir, ".hive")
	if err := os.MkdirAll(hiveDir, 0755); err != nil {
		t.Fatalf("Failed to create hive dir: %v", err)
	}

	logger := log.New(os.Stderr, "[TEST] ", log.LstdFlags)

	cfg := &config.Config{
		ProjectDir: tempDir,
		HiveDir:    ".hive",
		Queen: config.QueenConfig{
			Model:         "test-model",
			Provider:      "test",
			MaxIterations: 10,
		},
		Workers: config.WorkerConfig{
			MaxParallel:    2,
			MaxRetries:     2,
			DefaultTimeout: 5 * time.Minute,
			DefaultAdapter: "exec",
		},
		Adapters: map[string]config.AdapterConfig{
			"exec": {
				Command: "echo",
				Args:    []string{"test"},
			},
		},
	}

	// Create a queen instance
	q, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("Failed to create queen: %v", err)
	}

	// Manually set up a session with 'done' status
	sessionID := "test-session-done"
	if err := q.db.CreateSession(context.Background(), sessionID, "Test objective done"); err != nil {
		q.Close()
		t.Fatalf("Failed to create session: %v", err)
	}
	q.sessionID = sessionID

	// Set the status to 'done' directly in the database
	if err := q.db.UpdateSessionStatus(context.Background(), sessionID, "done"); err != nil {
		q.Close()
		t.Fatalf("Failed to set session status to done: %v", err)
	}

	// Verify status is 'done' before Close()
	session, err := q.db.GetSession(context.Background(), sessionID)
	if err != nil {
		q.Close()
		t.Fatalf("Failed to get session before close: %v", err)
	}
	if session.Status != "done" {
		q.Close()
		t.Fatalf("Expected status 'done' before Close(), got %q", session.Status)
	}

	// Call Close()
	if err := q.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	// Reopen database to verify status was preserved
	db, err := state.OpenDB(hiveDir)
	if err != nil {
		t.Fatalf("Failed to reopen DB: %v", err)
	}
	defer db.Close()

	// Verify status is still 'done' after Close()
	session, err = db.GetSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Failed to get session after close: %v", err)
	}

	if session.Status != "done" {
		t.Errorf("Expected status 'done' after Close(), got %q", session.Status)
	}

	t.Logf("✅ Close() preserved 'done' status correctly")
}

// TestCloseDoesNotOverwriteFailedStatus verifies that Close() does not overwrite
// a session status that is already 'failed'.
func TestCloseDoesNotOverwriteFailedStatus(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "queen-close-failed-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	hiveDir := filepath.Join(tempDir, ".hive")
	if err := os.MkdirAll(hiveDir, 0755); err != nil {
		t.Fatalf("Failed to create hive dir: %v", err)
	}

	logger := log.New(os.Stderr, "[TEST] ", log.LstdFlags)

	cfg := &config.Config{
		ProjectDir: tempDir,
		HiveDir:    ".hive",
		Queen: config.QueenConfig{
			Model:         "test-model",
			Provider:      "test",
			MaxIterations: 10,
		},
		Workers: config.WorkerConfig{
			MaxParallel:    2,
			MaxRetries:     2,
			DefaultTimeout: 5 * time.Minute,
			DefaultAdapter: "exec",
		},
		Adapters: map[string]config.AdapterConfig{
			"exec": {
				Command: "echo",
				Args:    []string{"test"},
			},
		},
	}

	// Create a queen instance
	q, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("Failed to create queen: %v", err)
	}

	// Manually set up a session with 'failed' status
	sessionID := "test-session-failed"
	if err := q.db.CreateSession(context.Background(), sessionID, "Test objective failed"); err != nil {
		q.Close()
		t.Fatalf("Failed to create session: %v", err)
	}
	q.sessionID = sessionID

	// Set the status to 'failed' directly in the database
	if err := q.db.UpdateSessionStatus(context.Background(), sessionID, "failed"); err != nil {
		q.Close()
		t.Fatalf("Failed to set session status to failed: %v", err)
	}

	// Verify status is 'failed' before Close()
	session, err := q.db.GetSession(context.Background(), sessionID)
	if err != nil {
		q.Close()
		t.Fatalf("Failed to get session before close: %v", err)
	}
	if session.Status != "failed" {
		q.Close()
		t.Fatalf("Expected status 'failed' before Close(), got %q", session.Status)
	}

	// Call Close()
	if err := q.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	// Reopen database to verify status was preserved
	db, err := state.OpenDB(hiveDir)
	if err != nil {
		t.Fatalf("Failed to reopen DB: %v", err)
	}
	defer db.Close()

	// Verify status is still 'failed' after Close()
	session, err = db.GetSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Failed to get session after close: %v", err)
	}

	if session.Status != "failed" {
		t.Errorf("Expected status 'failed' after Close(), got %q", session.Status)
	}

	t.Logf("✅ Close() preserved 'failed' status correctly")
}

// TestCloseSetsStoppedForRunningStatus verifies that Close() sets status to 'stopped'
// for a session that is still running (not in a terminal state).
func TestCloseSetsStoppedForRunningStatus(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "queen-close-running-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	hiveDir := filepath.Join(tempDir, ".hive")
	if err := os.MkdirAll(hiveDir, 0755); err != nil {
		t.Fatalf("Failed to create hive dir: %v", err)
	}

	logger := log.New(os.Stderr, "[TEST] ", log.LstdFlags)

	cfg := &config.Config{
		ProjectDir: tempDir,
		HiveDir:    ".hive",
		Queen: config.QueenConfig{
			Model:         "test-model",
			Provider:      "test",
			MaxIterations: 10,
		},
		Workers: config.WorkerConfig{
			MaxParallel:    2,
			MaxRetries:     2,
			DefaultTimeout: 5 * time.Minute,
			DefaultAdapter: "exec",
		},
		Adapters: map[string]config.AdapterConfig{
			"exec": {
				Command: "echo",
				Args:    []string{"test"},
			},
		},
	}

	// Create a queen instance
	q, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("Failed to create queen: %v", err)
	}

	// Manually set up a session with 'running' status
	sessionID := "test-session-running"
	if err := q.db.CreateSession(context.Background(), sessionID, "Test objective running"); err != nil {
		q.Close()
		t.Fatalf("Failed to create session: %v", err)
	}
	q.sessionID = sessionID

	// Verify status is 'running' before Close()
	session, err := q.db.GetSession(context.Background(), sessionID)
	if err != nil {
		q.Close()
		t.Fatalf("Failed to get session before close: %v", err)
	}
	if session.Status != "running" {
		q.Close()
		t.Fatalf("Expected status 'running' before Close(), got %q", session.Status)
	}

	// Call Close()
	if err := q.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	// Reopen database to verify status was set to 'stopped'
	db, err := state.OpenDB(hiveDir)
	if err != nil {
		t.Fatalf("Failed to reopen DB: %v", err)
	}
	defer db.Close()

	// Verify status is now 'stopped' after Close()
	session, err = db.GetSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Failed to get session after close: %v", err)
	}

	if session.Status != "stopped" {
		t.Errorf("Expected status 'stopped' after Close(), got %q", session.Status)
	}

	t.Logf("✅ Close() correctly set 'running' status to 'stopped'")
}
