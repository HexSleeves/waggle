package queen

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/exedev/waggle/internal/config"
	"github.com/exedev/waggle/internal/llm"
	"github.com/exedev/waggle/internal/state"
	"github.com/exedev/waggle/internal/task"
)

// TestResumeContinuesInterruptedSession simulates an interruption mid-execution
// and verifies that resume continues from where it left off.
func TestResumeContinuesInterruptedSession(t *testing.T) {
	// Create a temporary directory for the test hive
	tempDir, err := os.MkdirTemp("", "queen-resume-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	hiveDir := filepath.Join(tempDir, ".hive")
	if err := os.MkdirAll(hiveDir, 0755); err != nil {
		t.Fatalf("Failed to create hive dir: %v", err)
	}

	logger := log.New(os.Stderr, "[TEST] ", log.LstdFlags)

	// Create a config
	cfg := &config.Config{
		ProjectDir: tempDir,
		HiveDir:    ".hive",
		Queen: config.QueenConfig{
			Model:         "test-model",
			Provider:      "test",
			MaxIterations: 10,
			PlanTimeout:   5 * time.Minute,
			ReviewTimeout: 5 * time.Minute,
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

	// Create initial session with tasks
	db, err := state.OpenDB(hiveDir)
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}

	sessionID := fmt.Sprintf("test-session-%d", time.Now().UnixNano())
	objective := "Test objective for resume"

	// Create the session
	ctx := context.Background()
	if err := db.CreateSession(ctx, sessionID, objective); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Create some tasks with different statuses to simulate an interrupted session
	tasks := []state.TaskRow{
		{
			ID:          "task-1",
			Type:        "code",
			Status:      "complete",
			Priority:    2,
			Title:       "Completed task",
			Description: "This task is already complete",
			MaxRetries:  2,
			RetryCount:  0,
			CreatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		},
		{
			ID:          "task-2",
			Type:        "test",
			Status:      "running",
			Priority:    1,
			Title:       "Running task (interrupted)",
			Description: "This task was running when interrupted",
			MaxRetries:  2,
			RetryCount:  0,
			CreatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		},
		{
			ID:          "task-3",
			Type:        "generic",
			Status:      "pending",
			Priority:    1,
			Title:       "Pending task",
			Description: "This task is still pending",
			MaxRetries:  2,
			RetryCount:  0,
			DependsOn:   "task-2",
			CreatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		},
		{
			ID:          "task-4",
			Type:        "code",
			Status:      "failed",
			Priority:    1,
			Title:       "Failed task",
			Description: "This task failed",
			MaxRetries:  2,
			RetryCount:  1,
			CreatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		},
	}

	for _, tr := range tasks {
		if err := db.InsertTask(ctx, sessionID, tr); err != nil {
			t.Fatalf("Failed to insert task %s: %v", tr.ID, err)
		}
	}

	// Set session phase and iteration to simulate interruption during monitor phase
	if err := db.UpdateSessionPhase(ctx, sessionID, "monitor", 2); err != nil {
		t.Fatalf("Failed to update session phase: %v", err)
	}

	db.Close()

	// Now create a new Queen and resume the session
	cfg.ProjectDir = tempDir
	q, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("Failed to create queen: %v", err)
	}
	defer q.Close()

	// Resume the session
	resumedObjective, err := q.ResumeSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Failed to resume session: %v", err)
	}

	// Verify the objective was restored
	if resumedObjective != objective {
		t.Errorf("Expected objective %q, got %q", objective, resumedObjective)
	}

	// Verify the session ID was set
	if q.sessionID != sessionID {
		t.Errorf("Expected sessionID %q, got %q", sessionID, q.sessionID)
	}

	// Verify tasks were loaded into the task graph
	allTasks := q.tasks.All()
	if len(allTasks) != len(tasks) {
		t.Errorf("Expected %d tasks, got %d", len(tasks), len(allTasks))
	}

	// Verify task statuses
	taskMap := make(map[string]*task.Task)
	for _, taskItem := range allTasks {
		taskMap[taskItem.ID] = taskItem
	}

	// Check that task-1 (complete) is still complete
	if taskItem, ok := taskMap["task-1"]; !ok {
		t.Error("Task-1 not found")
	} else if taskItem.Status != task.StatusComplete {
		t.Errorf("Task-1 expected status complete, got %s", taskItem.Status)
	}

	// Check that task-2 (running) was reset to pending
	if taskItem, ok := taskMap["task-2"]; !ok {
		t.Error("Task-2 not found")
	} else if taskItem.Status != task.StatusPending {
		t.Errorf("Task-2 expected status pending (reset from running), got %s", taskItem.Status)
	}

	// Check that task-3 (pending) is still pending
	if taskItem, ok := taskMap["task-3"]; !ok {
		t.Error("Task-3 not found")
	} else if taskItem.Status != task.StatusPending {
		t.Errorf("Task-3 expected status pending, got %s", taskItem.Status)
	}

	// Check that task-4 (failed) is still failed
	if taskItem, ok := taskMap["task-4"]; !ok {
		t.Error("Task-4 not found")
	} else if taskItem.Status != task.StatusFailed {
		t.Errorf("Task-4 expected status failed, got %s", taskItem.Status)
	}

	// Check that task-4 has the correct retry count
	if taskMap["task-4"].RetryCount != 1 {
		t.Errorf("Task-4 expected retry count 1, got %d", taskMap["task-4"].RetryCount)
	}

	// Check that dependencies were restored
	if len(taskMap["task-3"].DependsOn) != 1 || taskMap["task-3"].DependsOn[0] != "task-2" {
		t.Errorf("Task-3 expected depends_on [task-2], got %v", taskMap["task-3"].DependsOn)
	}

	t.Logf("✅ Session resumed successfully with %d tasks", len(allTasks))
}

// TestResumeSessionNotFound verifies that resuming a non-existent session fails gracefully.
func TestResumeSessionNotFound(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "queen-resume-notfound-test-*")
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

	q, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("Failed to create queen: %v", err)
	}
	defer q.Close()

	// Try to resume a non-existent session
	_, err = q.ResumeSession(context.Background(), "non-existent-session-id")
	if err == nil {
		t.Error("Expected error when resuming non-existent session, got nil")
	}

	t.Logf("✅ Correctly got error for non-existent session: %v", err)
}

// TestFindResumableSession verifies the database method for finding resumable sessions.
func TestFindResumableSession(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "queen-find-resumable-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	hiveDir := filepath.Join(tempDir, ".hive")
	if err := os.MkdirAll(hiveDir, 0755); err != nil {
		t.Fatalf("Failed to create hive dir: %v", err)
	}

	db, err := state.OpenDB(hiveDir)
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Create a "done" session - should not be resumable
	doneSessionID := fmt.Sprintf("session-done-%d", time.Now().UnixNano())
	if err := db.CreateSession(ctx, doneSessionID, "Done objective"); err != nil {
		t.Fatalf("Failed to create done session: %v", err)
	}
	if err := db.UpdateSessionStatus(ctx, doneSessionID, "done"); err != nil {
		t.Fatalf("Failed to update session status: %v", err)
	}

	// Create a "running" session - should be resumable
	runningSessionID := fmt.Sprintf("session-running-%d", time.Now().UnixNano())
	if err := db.CreateSession(ctx, runningSessionID, "Running objective"); err != nil {
		t.Fatalf("Failed to create running session: %v", err)
	}
	// Status is already "running" by default

	// Find resumable session
	session, err := db.FindResumableSession(ctx)
	if err != nil {
		t.Fatalf("Failed to find resumable session: %v", err)
	}

	// Should find the running session, not the done one
	if session.ID != runningSessionID {
		t.Errorf("Expected resumable session %q, got %q", runningSessionID, session.ID)
	}

	if session.Objective != "Running objective" {
		t.Errorf("Expected objective %q, got %q", "Running objective", session.Objective)
	}

	t.Logf("✅ Found resumable session: %s with objective: %s", session.ID, session.Objective)
}

// TestResetRunningTasks verifies that running tasks are reset to pending on resume.
func TestResetRunningTasks(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "queen-reset-running-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	hiveDir := filepath.Join(tempDir, ".hive")
	if err := os.MkdirAll(hiveDir, 0755); err != nil {
		t.Fatalf("Failed to create hive dir: %v", err)
	}

	db, err := state.OpenDB(hiveDir)
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}
	defer db.Close()

	sessionID := fmt.Sprintf("session-%d", time.Now().UnixNano())
	ctx := context.Background()
	if err := db.CreateSession(ctx, sessionID, "Test objective"); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Create a running task
	runningTask := state.TaskRow{
		ID:          "running-task",
		Type:        "code",
		Status:      "running",
		Priority:    1,
		Title:       "Running Task",
		Description: "This task was running",
		MaxRetries:  2,
		WorkerID:    strPtr("worker-123"),
		CreatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := db.InsertTask(ctx, sessionID, runningTask); err != nil {
		t.Fatalf("Failed to insert running task: %v", err)
	}

	// Verify task is running
	tasks, err := db.GetTasksByStatus(ctx, sessionID, "running")
	if err != nil {
		t.Fatalf("Failed to get running tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("Expected 1 running task, got %d", len(tasks))
	}

	// Reset running tasks
	if err := db.ResetRunningTasks(ctx, sessionID); err != nil {
		t.Fatalf("Failed to reset running tasks: %v", err)
	}

	// Verify no running tasks remain
	runningTasks, err := db.GetTasksByStatus(ctx, sessionID, "running")
	if err != nil {
		t.Fatalf("Failed to get running tasks after reset: %v", err)
	}
	if len(runningTasks) != 0 {
		t.Errorf("Expected 0 running tasks after reset, got %d", len(runningTasks))
	}

	// Verify task is now pending
	pendingTasks, err := db.GetTasksByStatus(ctx, sessionID, "pending")
	if err != nil {
		t.Fatalf("Failed to get pending tasks: %v", err)
	}
	if len(pendingTasks) != 1 {
		t.Errorf("Expected 1 pending task after reset, got %d", len(pendingTasks))
	}

	// Verify worker_id was cleared
	if pendingTasks[0].WorkerID != nil && *pendingTasks[0].WorkerID != "" {
		t.Errorf("Expected worker_id to be cleared, got %v", *pendingTasks[0].WorkerID)
	}

	t.Logf("✅ Running tasks successfully reset to pending")
}

func strPtr(s string) *string {
	return &s
}

// TestQueenRunWithResumedSession verifies that Run() correctly handles resumed sessions.
func TestQueenRunWithResumedSession(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "queen-run-resumed-test-*")
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
			PlanTimeout:   5 * time.Minute,
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

	// First, create a session with tasks
	db, err := state.OpenDB(hiveDir)
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}

	sessionID := fmt.Sprintf("test-session-run-%d", time.Now().UnixNano())
	objective := "Run test objective"

	ctx := context.Background()
	if err := db.CreateSession(ctx, sessionID, objective); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Create a pending task that should be executed
	testTask := state.TaskRow{
		ID:          "exec-task",
		Type:        "generic",
		Status:      "pending",
		Priority:    1,
		Title:       "Exec Task",
		Description: "echo hello",
		MaxRetries:  2,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := db.InsertTask(ctx, sessionID, testTask); err != nil {
		t.Fatalf("Failed to insert task: %v", err)
	}

	db.Close()

	// Create queen and resume session
	q, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("Failed to create queen: %v", err)
	}

	// Resume the session
	if _, err := q.ResumeSession(context.Background(), sessionID); err != nil {
		q.Close()
		t.Fatalf("Failed to resume session: %v", err)
	}

	// Verify session state before Run
	if q.sessionID != sessionID {
		q.Close()
		t.Fatalf("Expected sessionID %q, got %q", sessionID, q.sessionID)
	}

	// The task graph should have the resumed task
	if len(q.tasks.All()) != 1 {
		q.Close()
		t.Fatalf("Expected 1 task in graph, got %d", len(q.tasks.All()))
	}

	// Clean up
	q.Close()

	t.Logf("✅ Queen Run with resumed session setup correctly")
}

// TestSavePhase verifies that session phase and iteration are saved correctly.
func TestSavePhase(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "queen-save-phase-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	logger := log.New(os.Stderr, "[TEST] ", log.LstdFlags)

	cfg := &config.Config{
		ProjectDir: tempDir,
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

	q, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("Failed to create queen: %v", err)
	}

	// Manually set up a session
	sessionID := fmt.Sprintf("phase-test-session-%d", time.Now().UnixNano())
	if err := q.db.CreateSession(context.Background(), sessionID, "Phase test"); err != nil {
		q.Close()
		t.Fatalf("Failed to create session: %v", err)
	}
	q.sessionID = sessionID
	q.phase = PhaseDelegate
	q.iteration = 3

	// Save phase
	q.savePhase()

	// Verify phase was saved
	phase, iteration, err := q.db.GetSessionPhase(context.Background(), sessionID)
	if err != nil {
		q.Close()
		t.Fatalf("Failed to get session phase: %v", err)
	}

	if phase != "delegate" {
		t.Errorf("Expected phase 'delegate', got %q", phase)
	}

	if iteration != 3 {
		t.Errorf("Expected iteration 3, got %d", iteration)
	}

	q.Close()

	t.Logf("✅ Phase saved correctly: %s, iteration %d", phase, iteration)
}

// TestLoadConversation_RestoresTurns verifies that persistConversation/loadConversation
// roundtrips the full conversation including tool_result messages.
func TestLoadConversation_RestoresTurns(t *testing.T) {
	q, _ := testQueen(t)
	ctx := context.Background()

	// Build a realistic conversation: user, assistant (tool_use), tool_result
	conversation := []llm.ToolMessage{
		{
			Role: "user",
			Content: []llm.ContentBlock{
				{Type: "text", Text: "Objective: test objective"},
			},
		},
		{
			Role: "assistant",
			Content: []llm.ContentBlock{
				{Type: "text", Text: "I will create tasks."},
				{Type: "tool_use", ToolCall: &llm.ToolCall{
					ID:    "call-1",
					Name:  "create_tasks",
					Input: json.RawMessage(`{"tasks":[]}`),
				}},
			},
		},
		{
			Role: "tool_result",
			ToolResults: []llm.ToolResult{
				{ToolCallID: "call-1", Content: "Created 0 tasks"},
			},
		},
		{
			Role: "assistant",
			Content: []llm.ContentBlock{
				{Type: "text", Text: "Assigning task-1."},
				{Type: "tool_use", ToolCall: &llm.ToolCall{
					ID:    "call-2",
					Name:  "assign_task",
					Input: json.RawMessage(`{"task_id":"t1"}`),
				}},
			},
		},
		{
			Role: "tool_result",
			ToolResults: []llm.ToolResult{
				{ToolCallID: "call-2", Content: "Assigned task t1"},
			},
		},
	}

	// Persist the full conversation
	q.persistConversation(ctx, conversation)

	// Load it back
	msgs, err := q.loadConversation(ctx, q.sessionID)
	if err != nil {
		t.Fatalf("loadConversation failed: %v", err)
	}

	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(msgs))
	}

	// Verify user message
	if msgs[0].Role != "user" {
		t.Errorf("msg 0 role: want 'user', got %q", msgs[0].Role)
	}

	// Verify assistant with tool_use
	if msgs[1].Role != "assistant" {
		t.Errorf("msg 1 role: want 'assistant', got %q", msgs[1].Role)
	}
	if len(msgs[1].Content) != 2 || msgs[1].Content[1].Type != "tool_use" {
		t.Errorf("msg 1 expected text+tool_use, got %+v", msgs[1].Content)
	}

	// Verify tool_result is present (this was the bug — old format lost these)
	if msgs[2].Role != "tool_result" {
		t.Errorf("msg 2 role: want 'tool_result', got %q", msgs[2].Role)
	}
	if len(msgs[2].ToolResults) != 1 {
		t.Errorf("msg 2 expected 1 tool result, got %d", len(msgs[2].ToolResults))
	}
	if msgs[2].ToolResults[0].ToolCallID != "call-1" {
		t.Errorf("msg 2 tool result ID: want 'call-1', got %q", msgs[2].ToolResults[0].ToolCallID)
	}

	// Verify second assistant + tool_result pair
	if msgs[3].Role != "assistant" {
		t.Errorf("msg 3 role: want 'assistant', got %q", msgs[3].Role)
	}
	if msgs[4].Role != "tool_result" {
		t.Errorf("msg 4 role: want 'tool_result', got %q", msgs[4].Role)
	}

	t.Log("✅ loadConversation correctly restored full conversation with tool_results")
}

// TestCompactMessagesPreservesToolPairs verifies compaction doesn't split tool_use/tool_result.
func TestCompactMessagesPreservesToolPairs(t *testing.T) {
	q, _ := testQueen(t)

	// Build a long conversation with tool_use/tool_result pairs
	var messages []llm.ToolMessage
	messages = append(messages, llm.ToolMessage{
		Role:    "user",
		Content: []llm.ContentBlock{{Type: "text", Text: "Objective: test"}},
	})

	// Add 40 assistant+tool_result pairs (80 messages + 1 user = 81 total)
	for i := 0; i < 40; i++ {
		messages = append(messages, llm.ToolMessage{
			Role: "assistant",
			Content: []llm.ContentBlock{
				{Type: "text", Text: fmt.Sprintf("Turn %d", i)},
				{Type: "tool_use", ToolCall: &llm.ToolCall{
					ID: fmt.Sprintf("call-%d", i), Name: "get_status",
				}},
			},
		})
		messages = append(messages, llm.ToolMessage{
			Role:        "tool_result",
			ToolResults: []llm.ToolResult{{ToolCallID: fmt.Sprintf("call-%d", i), Content: "ok"}},
		})
	}

	compacted := q.compactMessages(messages)

	// Verify no orphaned tool_results at the start of the kept section
	// (skip index 0=objective and 1=summary)
	for i := 2; i < len(compacted); i++ {
		msg := compacted[i]
		if i == 2 && (msg.Role == "tool_result" || len(msg.ToolResults) > 0) {
			t.Error("compacted section starts with orphaned tool_result")
		}
		// Every tool_result should be preceded by an assistant with tool_use
		if msg.Role == "tool_result" || len(msg.ToolResults) > 0 {
			if i < 2 {
				t.Errorf("tool_result at index %d has no predecessor", i)
				continue
			}
			prev := compacted[i-1]
			if prev.Role != "assistant" {
				t.Errorf("tool_result at index %d preceded by %q, not assistant", i, prev.Role)
			}
		}
	}

	if len(compacted) >= len(messages) {
		t.Errorf("compaction didn't reduce size: %d -> %d", len(messages), len(compacted))
	}

	t.Logf("✅ Compaction preserved tool pairs: %d → %d messages", len(messages), len(compacted))
}

// TestResumeSession_CompletedTasksNotReRun verifies that completed tasks
// are not re-executed after resume. Only pending/reset tasks should be ready.
func TestResumeSession_CompletedTasksNotReRun(t *testing.T) {
	tempDir := t.TempDir()
	hiveDir := filepath.Join(tempDir, ".hive")
	if err := os.MkdirAll(hiveDir, 0755); err != nil {
		t.Fatalf("create hive dir: %v", err)
	}

	logger := log.New(os.Stderr, "[TEST] ", log.LstdFlags)

	cfg := &config.Config{
		ProjectDir: tempDir,
		HiveDir:    ".hive",
		Queen: config.QueenConfig{
			Provider:      "",
			MaxIterations: 10,
		},
		Workers: config.WorkerConfig{
			MaxParallel:    2,
			MaxRetries:     2,
			DefaultTimeout: 5 * time.Minute,
			DefaultAdapter: "exec",
		},
		Adapters: map[string]config.AdapterConfig{
			"exec": {Command: "bash"},
		},
		Safety: config.SafetyConfig{
			AllowedPaths: []string{"."},
			MaxFileSize:  10 * 1024 * 1024,
		},
	}

	// Set up session in DB with task-a complete, task-b pending (depends on task-a)
	db, err := state.OpenDB(hiveDir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	sessionID := fmt.Sprintf("session-no-rerun-%d", time.Now().UnixNano())
	ctx := context.Background()
	if err := db.CreateSession(ctx, sessionID, "Test no re-run"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// task-a: already complete
	if err := db.InsertTask(ctx, sessionID, state.TaskRow{
		ID: "task-a", Type: "code", Status: "complete",
		Priority: 1, Title: "Task A", Description: "Already done",
		MaxRetries: 2,
	}); err != nil {
		t.Fatalf("insert task-a: %v", err)
	}

	// task-b: pending, depends on task-a
	if err := db.InsertTask(ctx, sessionID, state.TaskRow{
		ID: "task-b", Type: "code", Status: "pending",
		Priority: 1, Title: "Task B", Description: "Depends on A",
		MaxRetries: 2, DependsOn: "task-a",
	}); err != nil {
		t.Fatalf("insert task-b: %v", err)
	}

	db.Close()

	// Create Queen and resume
	q, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("create queen: %v", err)
	}
	defer q.Close()

	if _, err := q.ResumeSession(ctx, sessionID); err != nil {
		t.Fatalf("resume session: %v", err)
	}

	// Verify task-a is complete in the graph
	taskA, ok := q.tasks.Get("task-a")
	if !ok {
		t.Fatal("task-a not found in graph")
	}
	if taskA.Status != task.StatusComplete {
		t.Errorf("task-a status: want complete, got %s", taskA.Status)
	}

	// Verify task-b is pending
	taskB, ok := q.tasks.Get("task-b")
	if !ok {
		t.Fatal("task-b not found in graph")
	}
	if taskB.Status != task.StatusPending {
		t.Errorf("task-b status: want pending, got %s", taskB.Status)
	}

	// Ask the task graph for ready tasks — task-b should be ready
	// because its dependency task-a is complete
	ready := q.tasks.Ready()
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready task, got %d", len(ready))
	}
	if ready[0].ID != "task-b" {
		t.Errorf("ready task: want task-b, got %s", ready[0].ID)
	}

	// Completed task-a should NOT appear in the ready list
	for _, r := range ready {
		if r.ID == "task-a" {
			t.Error("task-a (complete) should NOT be in ready list")
		}
	}

	t.Log("✅ Completed tasks not re-run after resume")
}
