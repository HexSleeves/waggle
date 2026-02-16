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
	if err := db.CreateSession(ctx, "session-1", "First"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := db.CreateSession(ctx, "session-2", "Second"); err != nil {
		t.Fatal(err)
	}

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
	if err := db.CreateSession(ctx, "session-1", "Test"); err != nil {
		t.Fatal(err)
	}

	// Append events
	id1, err := db.AppendEvent(ctx, "session-1", "task.created", map[string]string{"task": "task-1"})
	if err != nil {
		t.Fatalf("AppendEvent failed: %v", err)
	}
	if id1 != 1 {
		t.Errorf("Expected event ID 1, got %d", id1)
	}

	id2, _ := db.AppendEvent(ctx, "session-1", "task.completed", map[string]string{"task": "task-1"})
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
	if err := db.CreateSession(ctx, "session-1", "Test"); err != nil {
		t.Fatal(err)
	}

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
	if err := db.CreateSession(ctx, "session-1", "Test"); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertTask(ctx, "session-1", TaskRow{ID: "task-1", Type: "code", Status: "pending", Title: "Test"}); err != nil {
		t.Fatal(err)
	}

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
	if err := db.CreateSession(ctx, "session-1", "Test"); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertTask(ctx, "session-1", TaskRow{ID: "task-1", Type: "code", Status: "pending", Title: "Test"}); err != nil {
		t.Fatal(err)
	}

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
	if err := db.CreateSession(ctx, "session-1", "Test"); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertTask(ctx, "session-1", TaskRow{ID: "task-1", Type: "code", Status: "pending", Title: "Test", RetryCount: 0}); err != nil {
		t.Fatal(err)
	}

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
	if err := db.CreateSession(ctx, "session-1", "Test"); err != nil {
		t.Fatal(err)
	}

	// Insert multiple tasks
	tasks := []TaskRow{
		{ID: "task-1", Type: "code", Status: "pending", Title: "Task 1", Priority: 1},
		{ID: "task-2", Type: "test", Status: "running", Title: "Task 2", Priority: 2},
		{ID: "task-3", Type: "review", Status: "complete", Title: "Task 3", Priority: 3},
	}

	for _, task := range tasks {
		if err := db.InsertTask(ctx, "session-1", task); err != nil {
			t.Fatal(err)
		}
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
	if err := db.CreateSession(ctx, "session-1", "Test"); err != nil {
		t.Fatal(err)
	}

	if err := db.InsertTask(ctx, "session-1", TaskRow{ID: "task-1", Type: "code", Status: "pending", Title: "Task 1"}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertTask(ctx, "session-1", TaskRow{ID: "task-2", Type: "code", Status: "running", Title: "Task 2"}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertTask(ctx, "session-1", TaskRow{ID: "task-3", Type: "code", Status: "pending", Title: "Task 3"}); err != nil {
		t.Fatal(err)
	}

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
	if err := db.CreateSession(ctx, "session-1", "Test"); err != nil {
		t.Fatal(err)
	}

	if err := db.InsertTask(ctx, "session-1", TaskRow{ID: "task-1", Type: "code", Status: "pending", Title: "Task 1"}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertTask(ctx, "session-1", TaskRow{ID: "task-2", Type: "code", Status: "pending", Title: "Task 2"}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertTask(ctx, "session-1", TaskRow{ID: "task-3", Type: "code", Status: "running", Title: "Task 3"}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertTask(ctx, "session-1", TaskRow{ID: "task-4", Type: "code", Status: "complete", Title: "Task 4"}); err != nil {
		t.Fatal(err)
	}

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
	if err := db.CreateSession(ctx, "session-1", "Test"); err != nil {
		t.Fatal(err)
	}

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
	if err := db.PostBlackboard(ctx, "session-1", "config", "{\"key\": \"updated\"}", "worker-2", "task-2", "config"); err != nil {
		t.Fatal(err)
	}

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
	if err := db.SetKV(ctx, "key1", "updated"); err != nil {
		t.Fatal(err)
	}
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

func TestListSessions(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := OpenDB(tmpDir)
	if err != nil {
		t.Fatalf("OpenDB failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// No sessions yet
	sessions, err := db.ListSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("Expected 0 sessions, got %d", len(sessions))
	}

	// Create sessions with a small delay so created_at ordering is deterministic
	if err := db.CreateSession(ctx, "s1", "First objective"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := db.CreateSession(ctx, "s2", "Second objective"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := db.CreateSession(ctx, "s3", "Third objective"); err != nil {
		t.Fatal(err)
	}

	// Add tasks to s1: 1 complete, 1 failed
	if err := db.InsertTask(ctx, "s1", TaskRow{ID: "t1", Type: "code", Status: "complete", Title: "T1"}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertTask(ctx, "s1", TaskRow{ID: "t2", Type: "code", Status: "failed", Title: "T2"}); err != nil {
		t.Fatal(err)
	}

	// Add tasks to s2: 2 pending, 1 complete
	if err := db.InsertTask(ctx, "s2", TaskRow{ID: "t3", Type: "code", Status: "pending", Title: "T3"}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertTask(ctx, "s2", TaskRow{ID: "t4", Type: "code", Status: "pending", Title: "T4"}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertTask(ctx, "s2", TaskRow{ID: "t5", Type: "code", Status: "complete", Title: "T5"}); err != nil {
		t.Fatal(err)
	}

	// s3 has no tasks

	// List all
	sessions, err = db.ListSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("Expected 3 sessions, got %d", len(sessions))
	}

	// Should be ordered by created_at DESC: s3, s2, s1
	if sessions[0].ID != "s3" {
		t.Errorf("Expected first session s3, got %s", sessions[0].ID)
	}
	if sessions[1].ID != "s2" {
		t.Errorf("Expected second session s2, got %s", sessions[1].ID)
	}
	if sessions[2].ID != "s1" {
		t.Errorf("Expected third session s1, got %s", sessions[2].ID)
	}

	// Verify s3 (no tasks)
	if sessions[0].TotalTasks != 0 {
		t.Errorf("s3: expected 0 total tasks, got %d", sessions[0].TotalTasks)
	}

	// Verify s2 counts
	if sessions[1].TotalTasks != 3 {
		t.Errorf("s2: expected 3 total tasks, got %d", sessions[1].TotalTasks)
	}
	if sessions[1].CompletedTasks != 1 {
		t.Errorf("s2: expected 1 completed, got %d", sessions[1].CompletedTasks)
	}
	if sessions[1].PendingTasks != 2 {
		t.Errorf("s2: expected 2 pending, got %d", sessions[1].PendingTasks)
	}
	if sessions[1].FailedTasks != 0 {
		t.Errorf("s2: expected 0 failed, got %d", sessions[1].FailedTasks)
	}

	// Verify s1 counts
	if sessions[2].TotalTasks != 2 {
		t.Errorf("s1: expected 2 total tasks, got %d", sessions[2].TotalTasks)
	}
	if sessions[2].CompletedTasks != 1 {
		t.Errorf("s1: expected 1 completed, got %d", sessions[2].CompletedTasks)
	}
	if sessions[2].FailedTasks != 1 {
		t.Errorf("s1: expected 1 failed, got %d", sessions[2].FailedTasks)
	}

	// Test limit
	sessions, err = db.ListSessions(ctx, 2)
	if err != nil {
		t.Fatalf("ListSessions with limit failed: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("Expected 2 sessions with limit=2, got %d", len(sessions))
	}
}

func TestListEvents(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := OpenDB(tmpDir)
	if err != nil {
		t.Fatalf("OpenDB failed: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.CreateSession(ctx, "session-1", "Test"); err != nil {
		t.Fatal(err)
	}

	// Append several events with different types
	id1, err := db.AppendEvent(ctx, "session-1", "task.created", map[string]string{"task_id": "t1"})
	if err != nil {
		t.Fatalf("AppendEvent failed: %v", err)
	}
	id2, err := db.AppendEvent(ctx, "session-1", "worker.spawned", map[string]string{"worker_id": "w1"})
	if err != nil {
		t.Fatalf("AppendEvent failed: %v", err)
	}
	id3, err := db.AppendEvent(ctx, "session-1", "task.status_changed", map[string]interface{}{"task_id": "t1", "payload": map[string]string{"new": "running"}})
	if err != nil {
		t.Fatalf("AppendEvent failed: %v", err)
	}
	id4, err := db.AppendEvent(ctx, "session-1", "worker.completed", map[string]string{"worker_id": "w1"})
	if err != nil {
		t.Fatalf("AppendEvent failed: %v", err)
	}

	// Also append an event to a different session to verify filtering
	if err := db.CreateSession(ctx, "session-2", "Other"); err != nil {
		t.Fatal(err)
	}
	_, _ = db.AppendEvent(ctx, "session-2", "task.created", map[string]string{"task_id": "t99"})

	// List all events for session-1
	events, err := db.ListEvents(ctx, "session-1", 100, 0)
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("Expected 4 events, got %d", len(events))
	}

	// Verify order (ascending by id)
	if events[0].ID != id1 || events[1].ID != id2 || events[2].ID != id3 || events[3].ID != id4 {
		t.Errorf("Events not in expected order: %v %v %v %v", events[0].ID, events[1].ID, events[2].ID, events[3].ID)
	}

	// Verify types
	if events[0].Type != "task.created" {
		t.Errorf("Expected type 'task.created', got %s", events[0].Type)
	}
	if events[1].Type != "worker.spawned" {
		t.Errorf("Expected type 'worker.spawned', got %s", events[1].Type)
	}

	// Verify session_id
	for _, e := range events {
		if e.SessionID != "session-1" {
			t.Errorf("Expected session_id 'session-1', got %s", e.SessionID)
		}
	}

	// Verify data is non-empty
	if events[0].Data == "" {
		t.Error("Expected non-empty data for first event")
	}

	// Test afterID filtering: get events after id2
	eventsAfter, err := db.ListEvents(ctx, "session-1", 100, id2)
	if err != nil {
		t.Fatalf("ListEvents with afterID failed: %v", err)
	}
	if len(eventsAfter) != 2 {
		t.Fatalf("Expected 2 events after id %d, got %d", id2, len(eventsAfter))
	}
	if eventsAfter[0].ID != id3 {
		t.Errorf("Expected first event after id2 to be id3=%d, got %d", id3, eventsAfter[0].ID)
	}

	// Test limit
	limitedEvents, err := db.ListEvents(ctx, "session-1", 2, 0)
	if err != nil {
		t.Fatalf("ListEvents with limit failed: %v", err)
	}
	if len(limitedEvents) != 2 {
		t.Fatalf("Expected 2 events with limit=2, got %d", len(limitedEvents))
	}
	if limitedEvents[0].ID != id1 || limitedEvents[1].ID != id2 {
		t.Error("Limited events should be the first two")
	}

	// Test filtering by session: session-2 should have only 1 event
	session2Events, err := db.ListEvents(ctx, "session-2", 100, 0)
	if err != nil {
		t.Fatalf("ListEvents for session-2 failed: %v", err)
	}
	if len(session2Events) != 1 {
		t.Fatalf("Expected 1 event for session-2, got %d", len(session2Events))
	}
}
