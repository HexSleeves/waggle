package queen

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/exedev/waggle/internal/blackboard"
	"github.com/exedev/waggle/internal/bus"
	"github.com/exedev/waggle/internal/config"
	"github.com/exedev/waggle/internal/llm"
	"github.com/exedev/waggle/internal/safety"
	"github.com/exedev/waggle/internal/state"
	"github.com/exedev/waggle/internal/task"
	"github.com/exedev/waggle/internal/worker"
)

// testQueen creates a minimal Queen suitable for handler tests.
// It uses a real TaskGraph, in-memory DB, and no adapters.
func testQueen(t *testing.T) (*Queen, string) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "queen-tools-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	hiveDir := filepath.Join(tmpDir, ".hive")
	if err := os.MkdirAll(hiveDir, 0755); err != nil {
		t.Fatalf("create hive dir: %v", err)
	}

	db, err := state.OpenDB(hiveDir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	msgBus := bus.New(100)
	board := blackboard.New(msgBus)
	tasks := task.NewTaskGraph(msgBus)

	// Minimal pool with no-op factory (tests that need spawning will skip)
	pool := worker.NewPool(4, func(id, adapter string) (worker.Bee, error) {
		return nil, nil
	}, msgBus)

	cfg := &config.Config{
		ProjectDir: tmpDir,
		HiveDir:    ".hive",
		Queen:      config.QueenConfig{MaxIterations: 10},
		Workers: config.WorkerConfig{
			MaxParallel:    4,
			MaxRetries:     2,
			DefaultTimeout: 5 * time.Minute,
			DefaultAdapter: "exec",
		},
		Safety: config.SafetyConfig{
			AllowedPaths: []string{"."},
			MaxFileSize:  10 * 1024 * 1024,
		},
		Adapters: map[string]config.AdapterConfig{},
	}

	logger := log.New(os.Stderr, "[TEST] ", log.LstdFlags)
	sessionID := "test-session"
	if err := db.CreateSession(context.Background(), sessionID, "test objective"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	guard, err := safety.NewGuard(cfg.Safety, cfg.ProjectDir)
	if err != nil {
		t.Fatalf("create safety guard: %v", err)
	}

	q := &Queen{
		cfg:         cfg,
		bus:         msgBus,
		db:          db,
		board:       board,
		tasks:       tasks,
		pool:        pool,
		guard:       guard,
		phase:       PhasePlan,
		logger:      logger,
		sessionID:   sessionID,
		assignments: make(map[string]string),
	}

	return q, tmpDir
}

func toJSON(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// --- queenTools ---

func TestQueenToolsReturnsAllTools(t *testing.T) {
	tools := queenTools()
	expected := []string{
		"create_tasks", "assign_task", "get_status", "get_task_output",
		"approve_task", "reject_task", "wait_for_workers",
		"read_file", "list_files", "complete", "fail",
	}
	if len(tools) != len(expected) {
		t.Fatalf("expected %d tools, got %d", len(expected), len(tools))
	}
	names := map[string]bool{}
	for _, td := range tools {
		names[td.Name] = true
	}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool %q", name)
		}
	}
}

// --- executeTool ---

func TestExecuteToolUnknown(t *testing.T) {
	q, _ := testQueen(t)
	_, err := q.executeTool(context.Background(), &llm.ToolCall{
		ID: "1", Name: "nonexistent", Input: json.RawMessage(`{}`),
	})
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("expected unknown tool error, got: %v", err)
	}
}

// --- create_tasks ---

func TestCreateTasksBasic(t *testing.T) {
	q, _ := testQueen(t)
	ctx := context.Background()

	input := toJSON(map[string]interface{}{
		"tasks": []map[string]interface{}{
			{"id": "t1", "title": "Task 1", "description": "Do thing 1", "type": "code", "priority": 2},
			{"id": "t2", "title": "Task 2", "description": "Do thing 2", "type": "test", "depends_on": []string{"t1"}},
		},
	})

	result, err := handleCreateTasks(ctx, q, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "Created 2 task(s)") {
		t.Fatalf("unexpected result: %s", result.LLMContent)
	}

	// Verify tasks are in graph
	if _, ok := q.tasks.Get("t1"); !ok {
		t.Error("t1 not found in task graph")
	}
	if _, ok := q.tasks.Get("t2"); !ok {
		t.Error("t2 not found in task graph")
	}

	// Verify defaults applied
	t1, _ := q.tasks.Get("t1")
	if t1.MaxRetries != q.cfg.Workers.MaxRetries {
		t.Errorf("expected default max_retries=%d, got %d", q.cfg.Workers.MaxRetries, t1.MaxRetries)
	}
}

func TestCreateTasksEmptyArray(t *testing.T) {
	q, _ := testQueen(t)
	input := toJSON(map[string]interface{}{"tasks": []map[string]interface{}{}})
	_, err := handleCreateTasks(context.Background(), q, input)
	if err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("expected empty error, got: %v", err)
	}
}

func TestCreateTasksMissingFields(t *testing.T) {
	q, _ := testQueen(t)
	tests := []struct {
		name  string
		input map[string]interface{}
		want  string
	}{
		{"missing id", map[string]interface{}{"title": "x", "description": "x", "type": "code"}, "id is required"},
		{"missing title", map[string]interface{}{"id": "x", "description": "x", "type": "code"}, "title is required"},
		{"missing desc", map[string]interface{}{"id": "x", "title": "x", "type": "code"}, "description is required"},
		{"missing type", map[string]interface{}{"id": "x", "title": "x", "description": "x"}, "type is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := toJSON(map[string]interface{}{"tasks": []map[string]interface{}{tt.input}})
			_, err := handleCreateTasks(context.Background(), q, input)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("expected error containing %q, got: %v", tt.want, err)
			}
		})
	}
}

func TestCreateTasksDuplicateID(t *testing.T) {
	q, _ := testQueen(t)
	ctx := context.Background()

	// Pre-add a task
	q.tasks.Add(&task.Task{ID: "existing", Title: "Existing", Type: task.TypeCode, Status: task.StatusPending})

	input := toJSON(map[string]interface{}{
		"tasks": []map[string]interface{}{
			{"id": "existing", "title": "Dup", "description": "x", "type": "code"},
		},
	})
	_, err := handleCreateTasks(ctx, q, input)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected duplicate error, got: %v", err)
	}
}

func TestCreateTasksCycleDetection(t *testing.T) {
	q, _ := testQueen(t)
	ctx := context.Background()

	input := toJSON(map[string]interface{}{
		"tasks": []map[string]interface{}{
			{"id": "a", "title": "A", "description": "x", "type": "code", "depends_on": []string{"b"}},
			{"id": "b", "title": "B", "description": "x", "type": "code", "depends_on": []string{"a"}},
		},
	})
	_, err := handleCreateTasks(ctx, q, input)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got: %v", err)
	}

	// Tasks should have been rolled back
	if _, ok := q.tasks.Get("a"); ok {
		t.Error("task 'a' should have been removed after cycle detection")
	}
	if _, ok := q.tasks.Get("b"); ok {
		t.Error("task 'b' should have been removed after cycle detection")
	}
}

// --- assign_task ---

func TestAssignTaskNotFound(t *testing.T) {
	q, _ := testQueen(t)
	input := toJSON(map[string]interface{}{"task_id": "missing"})
	_, err := handleAssignTask(context.Background(), q, input)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got: %v", err)
	}
}

func TestAssignTaskNotPending(t *testing.T) {
	q, _ := testQueen(t)
	q.tasks.Add(&task.Task{ID: "t1", Title: "T", Type: task.TypeCode, Status: task.StatusRunning})
	input := toJSON(map[string]interface{}{"task_id": "t1"})
	_, err := handleAssignTask(context.Background(), q, input)
	if err == nil || !strings.Contains(err.Error(), "not pending") {
		t.Fatalf("expected not pending error, got: %v", err)
	}
}

func TestAssignTaskMissingID(t *testing.T) {
	q, _ := testQueen(t)
	input := toJSON(map[string]interface{}{})
	_, err := handleAssignTask(context.Background(), q, input)
	if err == nil || !strings.Contains(err.Error(), "task_id is required") {
		t.Fatalf("expected required error, got: %v", err)
	}
}

func TestAssignTaskUnmetDependency(t *testing.T) {
	q, _ := testQueen(t)
	q.tasks.Add(&task.Task{ID: "dep1", Title: "Dep", Type: task.TypeCode, Status: task.StatusPending})
	q.tasks.Add(&task.Task{ID: "t1", Title: "T", Type: task.TypeCode, Status: task.StatusPending, DependsOn: []string{"dep1"}})
	input := toJSON(map[string]interface{}{"task_id": "t1"})
	_, err := handleAssignTask(context.Background(), q, input)
	if err == nil || !strings.Contains(err.Error(), "unmet dependency") {
		t.Fatalf("expected dependency error, got: %v", err)
	}
}

// --- get_status ---

func TestGetStatus(t *testing.T) {
	q, _ := testQueen(t)
	q.tasks.Add(&task.Task{ID: "t1", Title: "Task 1", Type: task.TypeCode, Status: task.StatusPending})
	q.tasks.Add(&task.Task{ID: "t2", Title: "Task 2", Type: task.TypeTest, Status: task.StatusComplete})

	result, err := handleGetStatus(context.Background(), q, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "t1") || !strings.Contains(result.LLMContent, "t2") {
		t.Errorf("result should contain both task IDs: %s", result)
	}
	if !strings.Contains(result.LLMContent, "pending") || !strings.Contains(result.LLMContent, "complete") {
		t.Errorf("result should contain status values: %s", result)
	}
}

// --- get_task_output ---

func TestGetTaskOutputComplete(t *testing.T) {
	q, _ := testQueen(t)
	q.tasks.Add(&task.Task{
		ID: "t1", Title: "Task 1", Type: task.TypeCode, Status: task.StatusComplete,
		Result: &task.Result{Success: true, Output: "all good"},
	})

	result, err := handleGetTaskOutput(context.Background(), q, toJSON(map[string]interface{}{"task_id": "t1"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "all good") {
		t.Errorf("expected output in result: %s", result)
	}
}

func TestGetTaskOutputStillRunning(t *testing.T) {
	q, _ := testQueen(t)
	q.tasks.Add(&task.Task{ID: "t1", Title: "Task 1", Type: task.TypeCode, Status: task.StatusRunning})

	result, err := handleGetTaskOutput(context.Background(), q, toJSON(map[string]interface{}{"task_id": "t1"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "still running") {
		t.Errorf("expected 'still running' in result: %s", result)
	}
}

func TestGetTaskOutputNotFound(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleGetTaskOutput(context.Background(), q, toJSON(map[string]interface{}{"task_id": "nope"}))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got: %v", err)
	}
}

// --- approve_task ---

func TestApproveTask(t *testing.T) {
	q, _ := testQueen(t)
	q.tasks.Add(&task.Task{ID: "t1", Title: "Task 1", Type: task.TypeCode, Status: task.StatusComplete})

	result, err := handleApproveTask(context.Background(), q, toJSON(map[string]interface{}{
		"task_id":  "t1",
		"feedback": "Looks great!",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "approved") {
		t.Errorf("expected 'approved' in result: %s", result)
	}

	// Check blackboard has the feedback
	entry, ok := q.board.Read("approval-t1")
	if !ok {
		t.Error("expected approval entry on blackboard")
	} else if entry.Value != "Looks great!" {
		t.Errorf("expected feedback 'Looks great!', got %v", entry.Value)
	}
}

func TestApproveTaskNotFound(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleApproveTask(context.Background(), q, toJSON(map[string]interface{}{"task_id": "nope"}))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found, got: %v", err)
	}
}

// --- reject_task ---

func TestRejectTask(t *testing.T) {
	q, _ := testQueen(t)
	q.tasks.Add(&task.Task{
		ID: "t1", Title: "Task 1", Type: task.TypeCode,
		Status: task.StatusComplete, MaxRetries: 3, RetryCount: 0,
		Description: "Original description",
	})

	result, err := handleRejectTask(context.Background(), q, toJSON(map[string]interface{}{
		"task_id":  "t1",
		"feedback": "Missing tests",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "rejected") {
		t.Errorf("expected 'rejected' in result: %s", result)
	}

	// Verify task was re-queued
	t1, _ := q.tasks.Get("t1")
	if t1.Status != task.StatusPending {
		t.Errorf("expected pending, got %s", t1.Status)
	}
	if t1.RetryCount != 1 {
		t.Errorf("expected retry count 1, got %d", t1.RetryCount)
	}
	if !strings.Contains(t1.Description, "Missing tests") {
		t.Errorf("feedback should be appended to description: %s", t1.Description)
	}
}

func TestRejectTaskMaxRetriesExceeded(t *testing.T) {
	q, _ := testQueen(t)
	q.tasks.Add(&task.Task{
		ID: "t1", Title: "Task 1", Type: task.TypeCode,
		Status: task.StatusComplete, MaxRetries: 2, RetryCount: 2,
	})

	_, err := handleRejectTask(context.Background(), q, toJSON(map[string]interface{}{
		"task_id":  "t1",
		"feedback": "Still bad",
	}))
	if err == nil || !strings.Contains(err.Error(), "exhausted all retries") {
		t.Fatalf("expected max retries error, got: %v", err)
	}
}

func TestRejectTaskMissingFeedback(t *testing.T) {
	q, _ := testQueen(t)
	q.tasks.Add(&task.Task{ID: "t1", Title: "T", Type: task.TypeCode, Status: task.StatusComplete, MaxRetries: 2})

	_, err := handleRejectTask(context.Background(), q, toJSON(map[string]interface{}{"task_id": "t1"}))
	if err == nil || !strings.Contains(err.Error(), "feedback is required") {
		t.Fatalf("expected feedback required error, got: %v", err)
	}
}

// --- read_file ---

func TestReadFile(t *testing.T) {
	q, tmpDir := testQueen(t)

	// Write a test file
	testContent := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte(testContent), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	result, err := handleReadFile(context.Background(), q, toJSON(map[string]interface{}{"path": "test.txt"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.LLMContent != testContent {
		t.Errorf("expected file content, got: %q", result.LLMContent)
	}
}

func TestReadFileLineRange(t *testing.T) {
	q, tmpDir := testQueen(t)

	lines := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte(lines), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	result, err := handleReadFile(context.Background(), q, toJSON(map[string]interface{}{
		"path": "test.txt", "line_start": 2, "line_end": 4,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "line2") || !strings.Contains(result.LLMContent, "line4") {
		t.Errorf("expected lines 2-4 in result: %s", result)
	}
	if strings.Contains(result.LLMContent, "line1") || strings.Contains(result.LLMContent, "line5") {
		t.Errorf("should not contain line1 or line5: %s", result)
	}
}

func TestReadFileMissingPath(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleReadFile(context.Background(), q, toJSON(map[string]interface{}{}))
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("expected path required error, got: %v", err)
	}
}

// --- list_files ---

func TestListFiles(t *testing.T) {
	q, tmpDir := testQueen(t)

	// Create some files
	if err := os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("package a"), 0644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "sub"), 0755); err != nil {
		t.Fatalf("create sub dir: %v", err)
	}

	result, err := handleListFiles(context.Background(), q, toJSON(map[string]interface{}{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "a.go") || !strings.Contains(result.LLMContent, "b.txt") {
		t.Errorf("expected files listed: %s", result)
	}
}

func TestListFilesWithPattern(t *testing.T) {
	q, tmpDir := testQueen(t)

	if err := os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("package a"), 0644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}

	result, err := handleListFiles(context.Background(), q, toJSON(map[string]interface{}{"pattern": "*.go"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "a.go") {
		t.Errorf("expected a.go in result: %s", result)
	}
	if strings.Contains(result.LLMContent, "b.txt") {
		t.Errorf("should not contain b.txt: %s", result)
	}
}

// --- complete ---

func TestComplete(t *testing.T) {
	q, _ := testQueen(t)

	result, err := handleComplete(context.Background(), q, toJSON(map[string]interface{}{"summary": "All done"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.phase != PhaseDone {
		t.Errorf("expected phase Done, got %s", q.phase)
	}
	if !strings.Contains(result.LLMContent, "All done") {
		t.Errorf("expected summary in result: %s", result)
	}
}

func TestCompleteMissingSummary(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleComplete(context.Background(), q, toJSON(map[string]interface{}{}))
	if err == nil || !strings.Contains(err.Error(), "summary is required") {
		t.Fatalf("expected summary required error, got: %v", err)
	}
}

// --- fail ---

func TestFail(t *testing.T) {
	q, _ := testQueen(t)

	result, err := handleFail(context.Background(), q, toJSON(map[string]interface{}{"reason": "Cannot proceed"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.phase != PhaseFailed {
		t.Errorf("expected phase Failed, got %s", q.phase)
	}
	if !strings.Contains(result.LLMContent, "Cannot proceed") {
		t.Errorf("expected reason in result: %s", result)
	}
}

func TestFailMissingReason(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleFail(context.Background(), q, toJSON(map[string]interface{}{}))
	if err == nil || !strings.Contains(err.Error(), "reason is required") {
		t.Fatalf("expected reason required error, got: %v", err)
	}
}

func TestGetTaskOutput_SmallOutput(t *testing.T) {
	q, _ := testQueen(t)
	smallOutput := "small output here"
	q.tasks.Add(&task.Task{
		ID: "t1", Title: "Small", Type: task.TypeCode, Status: task.StatusComplete,
		Result: &task.Result{Success: true, Output: smallOutput},
	})

	result, err := handleGetTaskOutput(context.Background(), q, toJSON(map[string]interface{}{"task_id": "t1"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.LLMContent, smallOutput) {
		t.Errorf("expected verbatim output, got: %s", result)
	}
	// Should NOT contain truncation marker
	if strings.Contains(result.LLMContent, "truncated") {
		t.Errorf("small output should not be truncated: %s", result)
	}
}

func TestGetTaskOutput_LargeOutput(t *testing.T) {
	q, _ := testQueen(t)
	// Create output larger than 8KB
	largeOutput := strings.Repeat("x", 20*1024)
	q.tasks.Add(&task.Task{
		ID: "t-big", Title: "Big", Type: task.TypeCode, Status: task.StatusComplete,
		Result: &task.Result{Success: true, Output: largeOutput},
	})

	result, err := handleGetTaskOutput(context.Background(), q, toJSON(map[string]interface{}{"task_id": "t-big"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should contain truncation marker
	if !strings.Contains(result.LLMContent, "truncated") {
		t.Errorf("large output should be truncated")
	}
	// Should contain head and tail
	if !strings.Contains(result.LLMContent, "xxx") {
		t.Error("result should contain parts of the original output")
	}
	// Result should be much smaller than original
	if len(result.LLMContent) > 10*1024 {
		t.Errorf("truncated result too large: %d bytes", len(result.LLMContent))
	}
}

func TestTruncateLargeOutput_FileWritten(t *testing.T) {
	tmpDir := t.TempDir()
	hiveDir := filepath.Join(tmpDir, ".hive")
	if err := os.MkdirAll(hiveDir, 0755); err != nil {
		t.Fatal(err)
	}

	largeOutput := strings.Repeat("a", 20*1024)
	result := truncateLargeOutput(largeOutput, "test-task", hiveDir)

	// Verify the file was written
	outputPath := filepath.Join(hiveDir, "outputs", "test-task.log")
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("expected output file at %s, got error: %v", outputPath, err)
	}
	if string(data) != largeOutput {
		t.Errorf("file content mismatch: got %d bytes, want %d", len(data), len(largeOutput))
	}

	// Verify result is truncated
	if !strings.Contains(result, "truncated") {
		t.Error("result should contain truncation marker")
	}
	if strings.Contains(result, strings.Repeat("a", 5000)) {
		t.Error("result should not contain full output")
	}
}

func TestTruncateLargeOutput_SmallPassthrough(t *testing.T) {
	small := "just a small string"
	result := truncateLargeOutput(small, "task-1", "/tmp/nonexistent")
	if result != small {
		t.Errorf("small output should pass through unchanged, got: %s", result)
	}
}
