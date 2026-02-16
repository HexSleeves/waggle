package queen

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/exedev/waggle/internal/task"
)

// --- read_file extended tests ---

func TestHandleReadFile_LineStartOnly(t *testing.T) {
	q, tmpDir := testQueen(t)
	content := "line1\nline2\nline3\nline4\nline5\n"
	os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte(content), 0644)

	result, err := handleReadFile(context.Background(), q, toJSON(map[string]interface{}{
		"path": "test.txt", "line_start": 3,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should contain lines 3-5
	if !strings.Contains(result.LLMContent, "line3") {
		t.Errorf("expected line3 in output: %s", result.LLMContent)
	}
	if !strings.Contains(result.LLMContent, "line5") {
		t.Errorf("expected line5 in output: %s", result.LLMContent)
	}
	// Should NOT contain lines 1-2
	if strings.Contains(result.LLMContent, "line1") {
		t.Errorf("should not contain line1: %s", result.LLMContent)
	}
}

func TestHandleReadFile_LineEndOnly(t *testing.T) {
	q, tmpDir := testQueen(t)
	content := "line1\nline2\nline3\nline4\nline5\n"
	os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte(content), 0644)

	result, err := handleReadFile(context.Background(), q, toJSON(map[string]interface{}{
		"path": "test.txt", "line_end": 2,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should contain lines 1-2
	if !strings.Contains(result.LLMContent, "line1") {
		t.Errorf("expected line1: %s", result.LLMContent)
	}
	if !strings.Contains(result.LLMContent, "line2") {
		t.Errorf("expected line2: %s", result.LLMContent)
	}
	// Should NOT contain line 3+
	if strings.Contains(result.LLMContent, "line3") {
		t.Errorf("should not contain line3: %s", result.LLMContent)
	}
}

func TestHandleReadFile_LineNumbers(t *testing.T) {
	q, tmpDir := testQueen(t)
	content := "alpha\nbeta\ngamma\n"
	os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte(content), 0644)

	result, err := handleReadFile(context.Background(), q, toJSON(map[string]interface{}{
		"path": "test.txt", "line_start": 1, "line_end": 3,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Line numbers should be in the output
	lines := strings.Split(result.LLMContent, "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		// Each line should have "N | content" format
		if !strings.Contains(line, "|") {
			t.Errorf("expected '|' separator in line: %q", line)
		}
	}
}

func TestHandleReadFile_SecurityCheck(t *testing.T) {
	q, _ := testQueen(t)
	// Try to read outside project dir
	_, err := handleReadFile(context.Background(), q, toJSON(map[string]interface{}{
		"path": "../../../etc/passwd",
	}))
	if err == nil {
		t.Fatal("expected security error for path outside project")
	}
}

func TestHandleReadFile_NonExistentFile(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleReadFile(context.Background(), q, toJSON(map[string]interface{}{
		"path": "nonexistent.txt",
	}))
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

// --- list_files extended tests ---

func TestHandleListFiles_Empty(t *testing.T) {
	q, tmpDir := testQueen(t)
	// Create an empty subdirectory
	os.MkdirAll(filepath.Join(tmpDir, "emptydir"), 0755)

	result, err := handleListFiles(context.Background(), q, toJSON(map[string]interface{}{
		"path": "emptydir",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.LLMContent != "No files found." {
		t.Errorf("expected 'No files found.', got: %q", result.LLMContent)
	}
}

func TestHandleListFiles_PatternNoMatch(t *testing.T) {
	q, tmpDir := testQueen(t)
	os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("package a"), 0644)

	result, err := handleListFiles(context.Background(), q, toJSON(map[string]interface{}{
		"pattern": "*.py",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.LLMContent != "No files found." {
		t.Errorf("expected no files for *.py pattern, got: %q", result.LLMContent)
	}
}

func TestHandleListFiles_Subdirectory(t *testing.T) {
	q, tmpDir := testQueen(t)
	subDir := filepath.Join(tmpDir, "sub")
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(subDir, "file.go"), []byte("package sub"), 0644)

	result, err := handleListFiles(context.Background(), q, toJSON(map[string]interface{}{
		"path": "sub",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "file.go") {
		t.Errorf("expected file.go in listing: %s", result.LLMContent)
	}
}

func TestHandleListFiles_SecurityCheck(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleListFiles(context.Background(), q, toJSON(map[string]interface{}{
		"path": "../../..",
	}))
	if err == nil {
		t.Fatal("expected security error for path traversal")
	}
}

func TestHandleListFiles_DirectoryNotFound(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleListFiles(context.Background(), q, toJSON(map[string]interface{}{
		"path": "nonexistent_dir",
	}))
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}

func TestHandleListFiles_DefaultPath(t *testing.T) {
	q, tmpDir := testQueen(t)
	os.WriteFile(filepath.Join(tmpDir, "root.txt"), []byte("x"), 0644)

	// No path parameter - should default to "."
	result, err := handleListFiles(context.Background(), q, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "root.txt") {
		t.Errorf("expected root.txt in default listing: %s", result.LLMContent)
	}
}

// --- complete/fail extended tests ---

func TestHandleComplete_DryRun(t *testing.T) {
	q, _ := testQueen(t)
	q.cfg.Queen.DryRun = true

	result, err := handleComplete(context.Background(), q, toJSON(map[string]interface{}{
		"summary": "Dry run plan",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "Dry-run complete") {
		t.Errorf("expected dry-run message: %s", result.LLMContent)
	}
	if q.phase != PhaseDone {
		t.Errorf("expected phase Done, got %s", q.phase)
	}
}

func TestHandleComplete_InvalidJSON(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleComplete(context.Background(), q, json.RawMessage(`{invalid}`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestHandleFail_InvalidJSON(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleFail(context.Background(), q, json.RawMessage(`{invalid}`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// --- reject_task extended tests ---

func TestHandleRejectTask_InvalidJSON(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleRejectTask(context.Background(), q, json.RawMessage(`{invalid}`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestHandleRejectTask_MissingTaskID(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleRejectTask(context.Background(), q, toJSON(map[string]interface{}{
		"feedback": "bad",
	}))
	if err == nil || !strings.Contains(err.Error(), "task_id is required") {
		t.Fatalf("expected task_id required error, got: %v", err)
	}
}

func TestHandleRejectTask_NotFound(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleRejectTask(context.Background(), q, toJSON(map[string]interface{}{
		"task_id":  "missing",
		"feedback": "bad",
	}))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got: %v", err)
	}
}

// --- approve_task extended tests ---

func TestHandleApproveTask_NoFeedback(t *testing.T) {
	q, _ := testQueen(t)
	q.tasks.Add(&task.Task{ID: "t1", Title: "Task 1", Type: task.TypeCode, Status: task.StatusComplete})

	result, err := handleApproveTask(context.Background(), q, toJSON(map[string]interface{}{
		"task_id": "t1",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "approved") {
		t.Errorf("expected approved: %s", result.LLMContent)
	}
	// No feedback - should NOT post to blackboard
	_, found := q.board.Read("approval-t1")
	if found {
		t.Error("should not post to blackboard when no feedback")
	}
}

func TestHandleApproveTask_InvalidJSON(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleApproveTask(context.Background(), q, json.RawMessage(`{invalid}`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestHandleApproveTask_MissingTaskID(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleApproveTask(context.Background(), q, toJSON(map[string]interface{}{}))
	if err == nil || !strings.Contains(err.Error(), "task_id is required") {
		t.Fatalf("expected task_id required, got: %v", err)
	}
}

// --- get_task_output extended tests ---

func TestHandleGetTaskOutput_Failed(t *testing.T) {
	q, _ := testQueen(t)
	q.tasks.Add(&task.Task{
		ID: "t1", Title: "Task 1", Type: task.TypeCode, Status: task.StatusFailed,
		Result: &task.Result{Success: false, Errors: []string{"compile error", "test failed"}},
	})

	result, err := handleGetTaskOutput(context.Background(), q, toJSON(map[string]interface{}{"task_id": "t1"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "compile error") {
		t.Errorf("expected error in output: %s", result.LLMContent)
	}
	if !strings.Contains(result.LLMContent, "test failed") {
		t.Errorf("expected error in output: %s", result.LLMContent)
	}
}

func TestHandleGetTaskOutput_MissingTaskID(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleGetTaskOutput(context.Background(), q, toJSON(map[string]interface{}{}))
	if err == nil || !strings.Contains(err.Error(), "task_id is required") {
		t.Fatalf("expected task_id required, got: %v", err)
	}
}

func TestHandleGetTaskOutput_InvalidJSON(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleGetTaskOutput(context.Background(), q, json.RawMessage(`{invalid}`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestHandleGetTaskOutput_CompleteNoResult(t *testing.T) {
	q, _ := testQueen(t)
	q.tasks.Add(&task.Task{ID: "t1", Title: "No Output Task", Type: task.TypeCode, Status: task.StatusComplete})

	result, err := handleGetTaskOutput(context.Background(), q, toJSON(map[string]interface{}{"task_id": "t1"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "no output available") {
		t.Errorf("expected 'no output available': %s", result.LLMContent)
	}
}

// --- assign_task extended tests ---

func TestHandleAssignTask_DryRun(t *testing.T) {
	q, _ := testQueen(t)
	q.cfg.Queen.DryRun = true
	q.tasks.Add(&task.Task{ID: "t1", Title: "Task", Type: task.TypeCode, Status: task.StatusPending})

	_, err := handleAssignTask(context.Background(), q, toJSON(map[string]interface{}{"task_id": "t1"}))
	if err == nil {
		t.Fatal("expected dry-run error")
	}
	if !strings.Contains(err.Error(), "dry-run") {
		t.Errorf("expected dry-run error, got: %v", err)
	}
}

func TestHandleAssignTask_InvalidJSON(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleAssignTask(context.Background(), q, json.RawMessage(`{invalid}`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// --- create_tasks extended tests ---

func TestHandleCreateTasks_InvalidJSON(t *testing.T) {
	q, _ := testQueen(t)
	_, err := handleCreateTasks(context.Background(), q, json.RawMessage(`{invalid}`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestHandleCreateTasks_CustomMaxRetries(t *testing.T) {
	q, _ := testQueen(t)
	input := toJSON(map[string]interface{}{
		"tasks": []map[string]interface{}{{
			"id": "t1", "title": "Task", "description": "Do", "type": "code",
			"max_retries": 5,
		}},
	})

	_, err := handleCreateTasks(context.Background(), q, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task, _ := q.tasks.Get("t1")
	if task.MaxRetries != 5 {
		t.Errorf("expected max_retries=5, got %d", task.MaxRetries)
	}
}

func TestHandleCreateTasks_WithConstraints(t *testing.T) {
	q, _ := testQueen(t)
	input := toJSON(map[string]interface{}{
		"tasks": []map[string]interface{}{{
			"id": "t1", "title": "Task", "description": "Do", "type": "code",
			"constraints":   []string{"no breaking changes"},
			"allowed_paths": []string{"internal/"},
		}},
	})

	result, err := handleCreateTasks(context.Background(), q, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	task, _ := q.tasks.Get("t1")
	if len(task.Constraints) != 1 || task.Constraints[0] != "no breaking changes" {
		t.Errorf("expected constraint, got %v", task.Constraints)
	}
	if len(task.AllowedPaths) != 1 || task.AllowedPaths[0] != "internal/" {
		t.Errorf("expected allowed path, got %v", task.AllowedPaths)
	}

	// Verify Display content
	if !strings.Contains(result.Display, "t1") {
		t.Errorf("expected task ID in display: %s", result.Display)
	}
}

// --- get_status extended tests ---

func TestHandleGetStatus_Empty(t *testing.T) {
	q, _ := testQueen(t)
	result, err := handleGetStatus(context.Background(), q, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "tasks") {
		t.Errorf("expected tasks key in JSON: %s", result.LLMContent)
	}
	if result.Display == "" {
		t.Error("expected non-empty display")
	}
}

// --- wait_for_workers extended tests ---

func TestHandleWaitForWorkers_DryRun(t *testing.T) {
	q, _ := testQueen(t)
	q.cfg.Queen.DryRun = true

	result, err := handleWaitForWorkers(context.Background(), q, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "dry-run") {
		t.Errorf("expected dry-run message: %s", result.LLMContent)
	}
}

func TestHandleWaitForWorkers_NoWorkers(t *testing.T) {
	q, _ := testQueen(t)

	result, err := handleWaitForWorkers(context.Background(), q, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "No workers currently running") {
		t.Errorf("expected no workers message: %s", result.LLMContent)
	}
}

// --- ToolOutput tests ---

func TestToolOutput_DisplayContent(t *testing.T) {
	// When Display is set, use it
	o := ToolOutput{LLMContent: "full detail", Display: "compact"}
	if o.DisplayContent() != "compact" {
		t.Errorf("expected 'compact', got %q", o.DisplayContent())
	}

	// When Display is empty, fall back to LLMContent
	o2 := ToolOutput{LLMContent: "fallback"}
	if o2.DisplayContent() != "fallback" {
		t.Errorf("expected 'fallback', got %q", o2.DisplayContent())
	}
}
