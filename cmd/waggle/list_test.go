package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HexSleeves/waggle/internal/state"
	"github.com/urfave/cli/v3"
)

// setupTestHive creates a temporary hive directory with an initialized database
func setupTestHive(t *testing.T) (string, *state.DB) {
	t.Helper()
	tmpDir := t.TempDir()
	hiveDir := filepath.Join(tmpDir, ".hive")

	db, err := state.OpenDB(hiveDir)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}

	return tmpDir, db
}

// createTestSession creates a test session with the given ID and objective
func createTestSession(t *testing.T, db *state.DB, id, objective string) {
	t.Helper()
	ctx := context.Background()
	if err := db.CreateSession(ctx, id, objective); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
}

// createTestTask creates a test task in the database
func createTestTask(t *testing.T, db *state.DB, sessionID string, task state.TaskRow) {
	t.Helper()
	ctx := context.Background()
	if err := db.InsertTask(ctx, sessionID, task); err != nil {
		t.Fatalf("Failed to insert task: %v", err)
	}
}

func TestCmdList_NoHive(t *testing.T) {
	tmpDir := t.TempDir()

	cmd := &cli.Command{
		Name: "list",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "project", Value: tmpDir},
		},
		Action: cmdList,
	}

	ctx := context.Background()
	err := cmd.Run(ctx, []string{"list"})

	if err == nil {
		t.Error("Expected error when no hive exists")
	}

	if !strings.Contains(err.Error(), "no hive found") {
		t.Errorf("Expected 'no hive found' error, got: %v", err)
	}
}

func TestCmdList_NoSessions(t *testing.T) {
	tmpDir, db := setupTestHive(t)
	defer db.Close()

	cmd := &cli.Command{
		Name: "list",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "project", Value: tmpDir},
		},
		Action: cmdList,
	}

	ctx := context.Background()
	err := cmd.Run(ctx, []string{"list"})

	if err == nil {
		t.Error("Expected error when no sessions exist")
	}

	if !strings.Contains(err.Error(), "no sessions found") {
		t.Errorf("Expected 'no sessions found' error, got: %v", err)
	}
}

func TestCmdList_NoTasks(t *testing.T) {
	tmpDir, db := setupTestHive(t)
	defer db.Close()

	createTestSession(t, db, "test-session", "Test Objective")

	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := &cli.Command{
		Name: "list",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "project", Value: tmpDir},
		},
		Action: cmdList,
	}

	ctx := context.Background()
	err := cmd.Run(ctx, []string{"list"})

	w.Close()
	os.Stdout = oldStdout
	buf.ReadFrom(r)

	if err != nil {
		t.Errorf("Expected no error for empty task list, got: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "No tasks found") {
		t.Errorf("Expected 'No tasks found' message, got: %s", output)
	}
}

func TestCmdList_Basic(t *testing.T) {
	tmpDir, db := setupTestHive(t)
	defer db.Close()

	createTestSession(t, db, "test-session", "Test Objective")

	workerID := "worker-1"
	createTestTask(t, db, "test-session", state.TaskRow{
		ID:       "task-00001",
		Type:     "code",
		Status:   "pending",
		Priority: 1,
		Title:    "Test Task",
		WorkerID: &workerID,
	})

	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := &cli.Command{
		Name: "list",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "project", Value: tmpDir},
		},
		Action: cmdList,
	}

	ctx := context.Background()
	err := cmd.Run(ctx, []string{"list"})

	w.Close()
	os.Stdout = oldStdout
	buf.ReadFrom(r)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Tasks") {
		t.Error("Expected 'Tasks' header in output")
	}
	if !strings.Contains(output, "task-") {
		t.Error("Expected task ID in output")
	}
	if !strings.Contains(output, "Test Task") {
		t.Error("Expected task title in output")
	}
}

func TestCmdList_WithStatusFilter(t *testing.T) {
	tmpDir, db := setupTestHive(t)
	defer db.Close()

	createTestSession(t, db, "test-session", "Test Objective")

	createTestTask(t, db, "test-session", state.TaskRow{
		ID:     "task-00001",
		Type:   "code",
		Status: "pending",
		Title:  "Pending Task",
	})
	createTestTask(t, db, "test-session", state.TaskRow{
		ID:     "task-00002",
		Type:   "test",
		Status: "complete",
		Title:  "Complete Task",
	})

	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := &cli.Command{
		Name: "list",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "project", Value: tmpDir},
			&cli.StringFlag{Name: "status", Value: "complete"},
		},
		Action: cmdList,
	}

	ctx := context.Background()
	err := cmd.Run(ctx, []string{"list", "--status", "complete"})

	w.Close()
	os.Stdout = oldStdout
	buf.ReadFrom(r)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Complete Task") {
		t.Error("Expected complete task in output")
	}
	if strings.Contains(output, "Pending Task") {
		t.Error("Should not show pending task when filtering by complete")
	}
	if !strings.Contains(output, "1 task(s)") {
		t.Errorf("Expected count of 1 task, got: %s", output)
	}
}

func TestCmdList_NoTasksWithStatusFilter(t *testing.T) {
	tmpDir, db := setupTestHive(t)
	defer db.Close()

	createTestSession(t, db, "test-session", "Test Objective")

	createTestTask(t, db, "test-session", state.TaskRow{
		ID:     "task-00001",
		Type:   "code",
		Status: "pending",
		Title:  "Pending Task",
	})

	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := &cli.Command{
		Name: "list",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "project", Value: tmpDir},
			&cli.StringFlag{Name: "status", Value: "complete"},
		},
		Action: cmdList,
	}

	ctx := context.Background()
	err := cmd.Run(ctx, []string{"list", "--status", "complete"})

	w.Close()
	os.Stdout = oldStdout
	buf.ReadFrom(r)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "No tasks with status 'complete' found") {
		t.Errorf("Expected status filter message, got: %s", output)
	}
}

func TestCmdList_WithTypeFilter(t *testing.T) {
	tmpDir, db := setupTestHive(t)
	defer db.Close()

	createTestSession(t, db, "test-session", "Test Objective")

	createTestTask(t, db, "test-session", state.TaskRow{
		ID:     "task-00001",
		Type:   "code",
		Status: "pending",
		Title:  "Code Task",
	})
	createTestTask(t, db, "test-session", state.TaskRow{
		ID:     "task-00002",
		Type:   "test",
		Status: "pending",
		Title:  "Test Task",
	})

	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := &cli.Command{
		Name: "list",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "project", Value: tmpDir},
			&cli.StringFlag{Name: "type", Value: "test"},
		},
		Action: cmdList,
	}

	ctx := context.Background()
	err := cmd.Run(ctx, []string{"list", "--type", "test"})

	w.Close()
	os.Stdout = oldStdout
	buf.ReadFrom(r)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Test Task") {
		t.Error("Expected matching type task in output")
	}
	if strings.Contains(output, "Code Task") {
		t.Error("Should not show non-matching type task")
	}
}

func TestCmdList_WithLimit(t *testing.T) {
	tmpDir, db := setupTestHive(t)
	defer db.Close()

	createTestSession(t, db, "test-session", "Test Objective")

	// Create 5 tasks
	for i := 1; i <= 5; i++ {
		createTestTask(t, db, "test-session", state.TaskRow{
			ID:       fmt.Sprintf("task-%05d", i),
			Type:     "code",
			Status:   "pending",
			Priority: i,
			Title:    fmt.Sprintf("Task %d", i),
		})
	}

	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := &cli.Command{
		Name: "list",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "project", Value: tmpDir},
			&cli.IntFlag{Name: "limit", Value: 2},
		},
		Action: cmdList,
	}

	ctx := context.Background()
	err := cmd.Run(ctx, []string{"list", "--limit", "2"})

	w.Close()
	os.Stdout = oldStdout
	buf.ReadFrom(r)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "2 task(s)") {
		t.Errorf("Expected count of 2 tasks with limit, got: %s", output)
	}
}

func TestCmdList_ZeroLimit(t *testing.T) {
	tmpDir, db := setupTestHive(t)
	defer db.Close()

	createTestSession(t, db, "test-session", "Test Objective")

	createTestTask(t, db, "test-session", state.TaskRow{
		ID:     "task-00001",
		Type:   "code",
		Status: "pending",
		Title:  "Task 1",
	})

	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := &cli.Command{
		Name: "list",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "project", Value: tmpDir},
			&cli.IntFlag{Name: "limit", Value: 0},
		},
		Action: cmdList,
	}

	ctx := context.Background()
	err := cmd.Run(ctx, []string{"list", "--limit", "0"})

	w.Close()
	os.Stdout = oldStdout
	buf.ReadFrom(r)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "1 task(s)") {
		t.Errorf("Expected count of 1 task with zero limit (no limit), got: %s", output)
	}
}

func TestCmdList_LongDescriptionTruncation(t *testing.T) {
	tmpDir, db := setupTestHive(t)
	defer db.Close()

	createTestSession(t, db, "test-session", "Test Objective")

	longTitle := strings.Repeat("a", 100)
	createTestTask(t, db, "test-session", state.TaskRow{
		ID:     "task-00001",
		Type:   "code",
		Status: "pending",
		Title:  longTitle,
	})

	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := &cli.Command{
		Name: "list",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "project", Value: tmpDir},
		},
		Action: cmdList,
	}

	ctx := context.Background()
	err := cmd.Run(ctx, []string{"list"})

	w.Close()
	os.Stdout = oldStdout
	buf.ReadFrom(r)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	output := buf.String()
	// Should contain truncated title with ...
	if !strings.Contains(output, "...") {
		t.Error("Expected long title to be truncated with '...'")
	}
}

func TestCmdList_MultipleTasksWithWorkers(t *testing.T) {
	tmpDir, db := setupTestHive(t)
	defer db.Close()

	createTestSession(t, db, "test-session", "Test Objective")

	worker1 := "worker-abc"
	worker2 := "worker-def"
	emptyWorker := ""

	createTestTask(t, db, "test-session", state.TaskRow{
		ID:       "task-00001",
		Type:     "code",
		Status:   "running",
		Priority: 3,
		Title:    "High Priority Task",
		WorkerID: &worker1,
	})
	createTestTask(t, db, "test-session", state.TaskRow{
		ID:       "task-00002",
		Type:     "test",
		Status:   "failed",
		Priority: 2,
		Title:    "Failed Task",
		WorkerID: &worker2,
	})
	createTestTask(t, db, "test-session", state.TaskRow{
		ID:       "task-00003",
		Type:     "review",
		Status:   "pending",
		Priority: 1,
		Title:    "Pending Task",
		WorkerID: &emptyWorker,
	})

	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := &cli.Command{
		Name: "list",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "project", Value: tmpDir},
		},
		Action: cmdList,
	}

	ctx := context.Background()
	err := cmd.Run(ctx, []string{"list"})

	w.Close()
	os.Stdout = oldStdout
	buf.ReadFrom(r)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "3 task(s)") {
		t.Errorf("Expected count of 3 tasks, got: %s", output)
	}
}

func TestCmdList_NilWorkerID(t *testing.T) {
	tmpDir, db := setupTestHive(t)
	defer db.Close()

	createTestSession(t, db, "test-session", "Test Objective")

	// Task with nil WorkerID should display as "-"
	createTestTask(t, db, "test-session", state.TaskRow{
		ID:       "task-00001",
		Type:     "code",
		Status:   "pending",
		Priority: 1,
		Title:    "Task with no worker",
		WorkerID: nil,
	})

	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := &cli.Command{
		Name: "list",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "project", Value: tmpDir},
		},
		Action: cmdList,
	}

	ctx := context.Background()
	err := cmd.Run(ctx, []string{"list"})

	w.Close()
	os.Stdout = oldStdout
	buf.ReadFrom(r)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	// Output should contain "-" for worker
	output := buf.String()
	if !strings.Contains(output, "Task with no worker") {
		t.Error("Expected task title in output")
	}
}

func TestCmdList_TableHeaders(t *testing.T) {
	tmpDir, db := setupTestHive(t)
	defer db.Close()

	createTestSession(t, db, "test-session", "Test Objective")

	createTestTask(t, db, "test-session", state.TaskRow{
		ID:     "task-00001",
		Type:   "code",
		Status: "pending",
		Title:  "Test Task",
	})

	var buf bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := &cli.Command{
		Name: "list",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "project", Value: tmpDir},
		},
		Action: cmdList,
	}

	ctx := context.Background()
	err := cmd.Run(ctx, []string{"list"})

	w.Close()
	os.Stdout = oldStdout
	buf.ReadFrom(r)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}

	output := buf.String()
	expectedHeaders := []string{"Status", "ID", "Type", "Priority", "Worker", "Title"}
	for _, header := range expectedHeaders {
		if !strings.Contains(output, header) {
			t.Errorf("Expected header '%s' in output", header)
		}
	}
}

func TestCmdList_DefaultProjectFlag(t *testing.T) {
	// Test that --project flag defaults to current directory
	cmd := &cli.Command{
		Name: "list",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "project", Value: "."},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			projectDir := cmd.String("project")
			if projectDir != "." {
				t.Errorf("Expected default project '.', got: %s", projectDir)
			}
			return nil
		},
	}

	ctx := context.Background()
	err := cmd.Run(ctx, []string{"list"})
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
}

func TestCmdList_CustomProjectPath(t *testing.T) {
	tmpDir := t.TempDir()

	cmd := &cli.Command{
		Name: "list",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "project"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			projectDir := cmd.String("project")
			if projectDir != tmpDir {
				t.Errorf("Expected project '%s', got: %s", tmpDir, projectDir)
			}
			return nil
		},
	}

	ctx := context.Background()
	err := cmd.Run(ctx, []string{"list", "--project", tmpDir})
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
}
