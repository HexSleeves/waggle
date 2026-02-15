package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenDB(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := OpenDB(tmpDir)
	if err != nil {
		t.Fatalf("OpenDB failed: %v", err)
	}
	defer db.Close()

	if db == nil {
		t.Fatal("OpenDB returned nil")
	}
	if db.db == nil {
		t.Error("db.db is nil")
	}

	// Verify database file was created
	dbPath := filepath.Join(tmpDir, "hive.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("hive.db was not created")
	}
}

func TestOpenDBCreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	nestedDir := filepath.Join(tmpDir, "nested", "hive")

	db, err := OpenDB(nestedDir)
	if err != nil {
		t.Fatalf("OpenDB failed: %v", err)
	}
	defer db.Close()

	if _, err := os.Stat(nestedDir); os.IsNotExist(err) {
		t.Error("Nested directory was not created")
	}
}

func TestDBSessionOperations(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := OpenDB(tmpDir)
	if err != nil {
		t.Fatalf("OpenDB failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Create session
	err = db.CreateSession(ctx, "session-1", "Test Objective")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// Get session
	session, err := db.GetSession(ctx, "session-1")
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if session == nil {
		t.Fatal("GetSession returned nil")
	}
	if session.ID != "session-1" {
		t.Errorf("Expected ID 'session-1', got %s", session.ID)
	}
	if session.Objective != "Test Objective" {
		t.Errorf("Expected objective 'Test Objective', got %s", session.Objective)
	}
	if session.Status != "running" {
		t.Errorf("Expected status 'running', got %s", session.Status)
	}

	// Update status
	err = db.UpdateSessionStatus(ctx, "session-1", "completed")
	if err != nil {
		t.Fatalf("UpdateSessionStatus failed: %v", err)
	}

	session, _ = db.GetSession(ctx, "session-1")
	if session.Status != "completed" {
		t.Errorf("Expected status 'completed', got %s", session.Status)
	}
}

func TestDBGetSessionNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	db, _ := OpenDB(tmpDir)
	defer db.Close()

	ctx := context.Background()
	_, err := db.GetSession(ctx, "non-existent")
	if err == nil {
		t.Error("Expected error for non-existent session")
	}
}

func TestDBLatestSession(t *testing.T) {
	tmpDir := t.TempDir()
	db, _ := OpenDB(tmpDir)
	defer db.Close()

	ctx := context.Background()

	// No sessions yet
	_, err := db.LatestSession(ctx)
	if err == nil {
		t.Error("Expected error when no sessions exist")
	}

	// Create sessions
	db.CreateSession(ctx, "session-1", "First")
	time.Sleep(10 * time.Millisecond)
	db.CreateSession(ctx, "session-2", "Second")

	// Get latest
	latest, err := db.LatestSession(ctx)
	if err != nil {
		t.Fatalf("LatestSession failed: %v", err)
	}
	if latest.ID != "session-2" {
		t.Errorf("Expected latest session-2, got %s", latest.ID)
	}
	if latest.Objective != "Second" {
		t.Errorf("Expected objective 'Second', got %s", latest.Objective)
	}
}

func TestDBEventOperations(t *testing.T) {
	tmpDir := t.TempDir()
	db, _ := OpenDB(tmpDir)
	defer db.Close()

	ctx := context.Background()
	db.CreateSession(ctx, "session-1", "Test")

	// Append events
	id1, err := db.AppendEvent(ctx, "session-1", "task.created", map[string]string{"task": "task-1"})
	if err != nil {
		t.Fatalf("AppendEvent failed: %v", err)
	}
	if id1 != 1 {
		t.Errorf("Expected event ID 1, got %d", id1)
	}

	id2, err := db.AppendEvent(ctx, "session-1", "task.completed", map[string]string{"task": "task-1"})
	if id2 != 2 {
		t.Errorf("Expected event ID 2, got %d", id2)
	}

	// Count events
	count, err := db.EventCount(ctx, "session-1")
	if err != nil {
		t.Fatalf("EventCount failed: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected count 2, got %d", count)
	}

	// Append nil data
	id3, err := db.AppendEvent(ctx, "session-1", "ping", nil)
	if err != nil {
		t.Fatalf("AppendEvent with nil data failed: %v", err)
	}
	if id3 != 3 {
		t.Errorf("Expected event ID 3, got %d", id3)
	}
}

func TestDBTaskOperations(t *testing.T) {
	tmpDir := t.TempDir()
	db, _ := OpenDB(tmpDir)
	defer db.Close()

	ctx := context.Background()
	db.CreateSession(ctx, "session-1", "Test")

	// Insert task
	task := TaskRow{
		ID:          "task-1",
		SessionID:   "session-1",
		Type:        "code",
		Status:      "pending",
		Priority:    2,
		Title:       "Test Task",
		Description: "A test task",
		MaxRetries:  3,
		RetryCount:  0,
		DependsOn:   "",
	}

	err := db.InsertTask(ctx, "session-1", task)
	if err != nil {
		t.Fatalf("InsertTask failed: %v", err)
	}

	// Get task
	retrieved, err := db.GetTask(ctx, "session-1", "task-1")
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if retrieved.ID != "task-1" {
		t.Errorf("Expected ID 'task-1', got %s", retrieved.ID)
	}
	if retrieved.Title != "Test Task" {
		t.Errorf("Expected title 'Test Task', got %s", retrieved.Title)
	}
	if retrieved.Priority != 2 {
		t.Errorf("Expected priority 2, got %d", retrieved.Priority)
	}

	// Update status
	err = db.UpdateTaskStatus(ctx, "session-1", "task-1", "running")
	if err != nil {
		t.Fatalf("UpdateTaskStatus failed: %v", err)
	}

	retrieved, _ = db.GetTask(ctx, "session-1", "task-1")
	if retrieved.Status != "running" {
		t.Errorf("Expected status 'running', got %s", retrieved.Status)
	}
	if retrieved.StartedAt == nil {
		t.Error("Expected StartedAt to be set")
	}

	// Update to complete
	err = db.UpdateTaskStatus(ctx, "session-1", "task-1", "complete")
	if err != nil {
		t.Fatalf("UpdateTaskStatus failed: %v", err)
	}

	retrieved, _ = db.GetTask(ctx, "session-1", "task-1")
	if retrieved.Status != "complete" {
		t.Errorf("Expected status 'complete', got %s", retrieved.Status)
	}
	if retrieved.CompletedAt == nil {
		t.Error("Expected CompletedAt to be set")
	}
}

func TestDBUpdateTaskWorker(t *testing.T) {
	tmpDir := t.TempDir()
	db, _ := OpenDB(tmpDir)
	defer db.Close()

	ctx := context.Background()
	db.CreateSession(ctx, "session-1", "Test")
	db.InsertTask(ctx, "session-1", TaskRow{ID: "task-1", Type: "code", Status: "pending", Title: "Test"})

	err := db.UpdateTaskWorker(ctx, "session-1", "task-1", "worker-123")
	if err != nil {
		t.Fatalf("UpdateTaskWorker failed: %v", err)
	}

	retrieved, _ := db.GetTask(ctx, "session-1", "task-1")
	if retrieved.WorkerID == nil || *retrieved.WorkerID != "worker-123" {
		t.Errorf("Expected worker_id 'worker-123', got %v", retrieved.WorkerID)
	}
}

func TestDBUpdateTaskResult(t *testing.T) {
	tmpDir := t.TempDir()
	db, _ := OpenDB(tmpDir)
	defer db.Close()

	ctx := context.Background()
	db.CreateSession(ctx, "session-1", "Test")
	db.InsertTask(ctx, "session-1", TaskRow{ID: "task-1", Type: "code", Status: "pending", Title: "Test"})

	result := map[string]interface{}{
		"success": true,
		"output":  "build successful",
	}

	err := db.UpdateTaskResult(ctx, "session-1", "task-1", result)
	if err != nil {
		t.Fatalf("UpdateTaskResult failed: %v", err)
	}

	retrieved, _ := db.GetTask(ctx, "session-1", "task-1")
	if retrieved.Result == nil {
		t.Fatal("Expected result to be set")
	}
	if *retrieved.Result == "" {
		t.Error("Expected non-empty result")
	}
}

func TestDBIncrementTaskRetry(t *testing.T) {
	tmpDir := t.TempDir()
	db, _ := OpenDB(tmpDir)
	defer db.Close()

	ctx := context.Background()
	db.CreateSession(ctx, "session-1", "Test")
	db.InsertTask(ctx, "session-1", TaskRow{ID: "task-1", Type: "code", Status: "pending", Title: "Test", RetryCount: 0})

	count, err := db.IncrementTaskRetry(ctx, "session-1", "task-1")
	if err != nil {
		t.Fatalf("IncrementTaskRetry failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected retry count 1, got %d", count)
	}

	count, _ = db.IncrementTaskRetry(ctx, "session-1", "task-1")
	if count != 2 {
		t.Errorf("Expected retry count 2, got %d", count)
	}
}

func TestDBGetTasks(t *testing.T) {
	tmpDir := t.TempDir()
	db, _ := OpenDB(tmpDir)
	defer db.Close()

	ctx := context.Background()
	db.CreateSession(ctx, "session-1", "Test")

	// Insert multiple tasks
	tasks := []TaskRow{
		{ID: "task-1", Type: "code", Status: "pending", Title: "Task 1", Priority: 1},
		{ID: "task-2", Type: "test", Status: "running", Title: "Task 2", Priority: 2},
		{ID: "task-3", Type: "review", Status: "complete", Title: "Task 3", Priority: 3},
	}

	for _, task := range tasks {
		db.InsertTask(ctx, "session-1", task)
	}

	// Get all tasks
	retrieved, err := db.GetTasks(ctx, "session-1")
	if err != nil {
		t.Fatalf("GetTasks failed: %v", err)
	}
	if len(retrieved) != 3 {
		t.Errorf("Expected 3 tasks, got %d", len(retrieved))
	}

	// Verify ordering by priority desc
	if retrieved[0].Priority < retrieved[1].Priority {
		t.Error("Tasks should be ordered by priority desc")
	}
}

func TestDBGetTasksByStatus(t *testing.T) {
	tmpDir := t.TempDir()
	db, _ := OpenDB(tmpDir)
	defer db.Close()

	ctx := context.Background()
	db.CreateSession(ctx, "session-1", "Test")

	db.InsertTask(ctx, "session-1", TaskRow{ID: "task-1", Type: "code", Status: "pending", Title: "Task 1"})
	db.InsertTask(ctx, "session-1", TaskRow{ID: "task-2", Type: "code", Status: "running", Title: "Task 2"})
	db.InsertTask(ctx, "session-1", TaskRow{ID: "task-3", Type: "code", Status: "pending", Title: "Task 3"})

	pending, err := db.GetTasksByStatus(ctx, "session-1", "pending")
	if err != nil {
		t.Fatalf("GetTasksByStatus failed: %v", err)
	}
	if len(pending) != 2 {
		t.Errorf("Expected 2 pending tasks, got %d", len(pending))
	}

	running, _ := db.GetTasksByStatus(ctx, "session-1", "running")
	if len(running) != 1 {
		t.Errorf("Expected 1 running task, got %d", len(running))
	}
}

func TestDBCountTasksByStatus(t *testing.T) {
	tmpDir := t.TempDir()
	db, _ := OpenDB(tmpDir)
	defer db.Close()

	ctx := context.Background()
	db.CreateSession(ctx, "session-1", "Test")

	db.InsertTask(ctx, "session-1", TaskRow{ID: "task-1", Type: "code", Status: "pending", Title: "Task 1"})
	db.InsertTask(ctx, "session-1", TaskRow{ID: "task-2", Type: "code", Status: "pending", Title: "Task 2"})
	db.InsertTask(ctx, "session-1", TaskRow{ID: "task-3", Type: "code", Status: "running", Title: "Task 3"})
	db.InsertTask(ctx, "session-1", TaskRow{ID: "task-4", Type: "code", Status: "complete", Title: "Task 4"})

	counts, err := db.CountTasksByStatus(ctx, "session-1")
	if err != nil {
		t.Fatalf("CountTasksByStatus failed: %v", err)
	}

	if counts["pending"] != 2 {
		t.Errorf("Expected 2 pending, got %d", counts["pending"])
	}
	if counts["running"] != 1 {
		t.Errorf("Expected 1 running, got %d", counts["running"])
	}
	if counts["complete"] != 1 {
		t.Errorf("Expected 1 complete, got %d", counts["complete"])
	}
}

func TestDBBlackboardOperations(t *testing.T) {
	tmpDir := t.TempDir()
	db, _ := OpenDB(tmpDir)
	defer db.Close()

	ctx := context.Background()
	db.CreateSession(ctx, "session-1", "Test")

	// Post entry
	err := db.PostBlackboard(ctx, "session-1", "config", "{\"key\": \"value\"}", "worker-1", "task-1", "config,important")
	if err != nil {
		t.Fatalf("PostBlackboard failed: %v", err)
	}

	// Read entry
	value, err := db.ReadBlackboard(ctx, "session-1", "config")
	if err != nil {
		t.Fatalf("ReadBlackboard failed: %v", err)
	}
	if value != "{\"key\": \"value\"}" {
		t.Errorf("Expected '{\"key\": \"value\"}', got %s", value)
	}

	// Update entry
	db.PostBlackboard(ctx, "session-1", "config", "{\"key\": \"updated\"}", "worker-2", "task-2", "config")

	value, _ = db.ReadBlackboard(ctx, "session-1", "config")
	if value != "{\"key\": \"updated\"}" {
		t.Errorf("Expected updated value, got %s", value)
	}
}

func TestDBKVMemoryOperations(t *testing.T) {
	tmpDir := t.TempDir()
	db, _ := OpenDB(tmpDir)
	defer db.Close()

	ctx := context.Background()

	// Set KV
	err := db.SetKV(ctx, "key1", "value1")
	if err != nil {
		t.Fatalf("SetKV failed: %v", err)
	}

	// Get KV
	value, err := db.GetKV(ctx, "key1")
	if err != nil {
		t.Fatalf("GetKV failed: %v", err)
	}
	if value != "value1" {
		t.Errorf("Expected 'value1', got %s", value)
	}

	// Update KV
	db.SetKV(ctx, "key1", "updated")
	value, _ = db.GetKV(ctx, "key1")
	if value != "updated" {
		t.Errorf("Expected 'updated', got %s", value)
	}

	// Get non-existent
	_, err = db.GetKV(ctx, "non-existent")
	if err == nil {
		t.Error("Expected error for non-existent key")
	}
}

func TestDBClose(t *testing.T) {
	tmpDir := t.TempDir()
	db, _ := OpenDB(tmpDir)

	err := db.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestDBRaw(t *testing.T) {
	tmpDir := t.TempDir()
	db, _ := OpenDB(tmpDir)
	defer db.Close()

	raw := db.Raw()
	if raw == nil {
		t.Error("Raw() returned nil")
	}
}

func TestTaskRowStruct(t *testing.T) {
	workerID := "worker-1"
	result := "{\"success\": true}"
	startedAt := "2024-01-01T00:00:00Z"
	completedAt := "2024-01-01T01:00:00Z"

	tr := TaskRow{
		ID:          "task-1",
		SessionID:   "session-1",
		Type:        "code",
		Status:      "complete",
		Priority:    2,
		Title:       "Test",
		Description: "Test desc",
		WorkerID:    &workerID,
		Result:      &result,
		MaxRetries:  3,
		RetryCount:  1,
		DependsOn:   "task-0",
		CreatedAt:   "2024-01-01T00:00:00Z",
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
	}

	if tr.ID != "task-1" {
		t.Error("ID mismatch")
	}
	if *tr.WorkerID != "worker-1" {
		t.Error("WorkerID mismatch")
	}
}

func TestSessionInfoStruct(t *testing.T) {
	si := SessionInfo{
		ID:        "session-1",
		Objective: "Test",
		Status:    "running",
		CreatedAt: "2024-01-01T00:00:00Z",
		UpdatedAt: "2024-01-01T00:00:00Z",
	}

	if si.ID != "session-1" {
		t.Error("ID mismatch")
	}
	if si.Objective != "Test" {
		t.Error("Objective mismatch")
	}
}
