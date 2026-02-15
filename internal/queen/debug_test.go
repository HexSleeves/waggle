package queen

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/exedev/queen-bee/internal/config"
	"github.com/exedev/queen-bee/internal/state"
)

func TestDebugResume(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "queen-debug-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	hiveDir := filepath.Join(tempDir, ".hive")
	if err := os.MkdirAll(hiveDir, 0755); err != nil {
		t.Fatalf("Failed to create hive dir: %v", err)
	}

	t.Logf("Test hiveDir: %s", hiveDir)

	// Create DB directly
	db, err := state.OpenDB(hiveDir)
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}

	sessionID := fmt.Sprintf("test-session-%d", time.Now().UnixNano())
	objective := "Test objective"

	ctx := context.Background()
	if err := db.CreateSession(ctx, sessionID, objective); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Verify session exists
	session, err := db.GetSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("Failed to get session right after creation: %v", err)
	}
	t.Logf("Created session: %s, objective: %s", session.ID, session.Objective)

	// Check database file location
	dbPath := filepath.Join(hiveDir, "hive.db")
	t.Logf("DB file exists at test location: %v", fileExists(dbPath))

	db.Close()

	// Now create a config like the test does and check HivePath
	cfg := &config.Config{
		ProjectDir: tempDir,
		// HiveDir is empty - will use default ".hive"
	}
	t.Logf("cfg.HivePath() = %s", cfg.HivePath())

	// Check if DB file exists at what will be Queen's location
	queenHiveDir := cfg.HivePath()
	queenDBPath := filepath.Join(queenHiveDir, "hive.db")
	t.Logf("Queen DB path: %s", queenDBPath)
	t.Logf("DB file exists at Queen's location: %v", fileExists(queenDBPath))

	// Also try to open DB from Queen's location and check session
	queenDB, err := state.OpenDB(queenHiveDir)
	if err != nil {
		t.Fatalf("Failed to open Queen's DB: %v", err)
	}
	defer queenDB.Close()

	session2, err := queenDB.GetSession(ctx, sessionID)
	if err != nil {
		t.Logf("ERROR: Failed to get session from Queen's DB: %v", err)
	} else {
		t.Logf("SUCCESS: Got session from Queen's DB: %s", session2.ID)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}
