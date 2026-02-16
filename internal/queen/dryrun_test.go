package queen

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestDryRunBlocksAssignment verifies assign_task returns an error in dry-run mode.
func TestDryRunBlocksAssignment(t *testing.T) {
	q, _ := testQueen(t)
	q.cfg.Queen.DryRun = true

	input := toJSON(map[string]interface{}{"task_id": "t1"})
	_, err := handleAssignTask(context.Background(), q, input)
	if err == nil {
		t.Fatal("expected error from assign_task in dry-run mode, got nil")
	}
	if !strings.Contains(err.Error(), "dry-run mode") {
		t.Errorf("expected dry-run error message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "task assignment blocked") {
		t.Errorf("expected 'task assignment blocked' in error, got: %v", err)
	}
}

// TestDryRunBlocksWait verifies wait_for_workers returns a message in dry-run mode.
func TestDryRunBlocksWait(t *testing.T) {
	q, _ := testQueen(t)
	q.cfg.Queen.DryRun = true

	result, err := handleWaitForWorkers(context.Background(), q, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "dry-run mode") {
		t.Errorf("expected dry-run message, got: %s", result.LLMContent)
	}
	if !strings.Contains(result.LLMContent, "no workers running") {
		t.Errorf("expected 'no workers running' in result, got: %s", result.LLMContent)
	}
}

// TestDryRunAllowsPlanning verifies create_tasks works normally in dry-run mode.
func TestDryRunAllowsPlanning(t *testing.T) {
	q, _ := testQueen(t)
	q.cfg.Queen.DryRun = true

	input := toJSON(map[string]interface{}{
		"tasks": []map[string]interface{}{
			{"id": "t1", "title": "Task 1", "description": "Do thing 1", "type": "code", "priority": 3},
			{"id": "t2", "title": "Task 2", "description": "Do thing 2", "type": "test", "depends_on": []string{"t1"}},
		},
	})

	result, err := handleCreateTasks(context.Background(), q, input)
	if err != nil {
		t.Fatalf("create_tasks should work in dry-run mode, got error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "Created 2 task(s)") {
		t.Errorf("unexpected result: %s", result.LLMContent)
	}

	// Verify tasks are in graph
	if _, ok := q.tasks.Get("t1"); !ok {
		t.Error("t1 not found in task graph")
	}
	if _, ok := q.tasks.Get("t2"); !ok {
		t.Error("t2 not found in task graph")
	}
}

// TestDryRunAllowsGetStatus verifies get_status works in dry-run mode.
func TestDryRunAllowsGetStatus(t *testing.T) {
	q, _ := testQueen(t)
	q.cfg.Queen.DryRun = true

	// Create some tasks first
	input := toJSON(map[string]interface{}{
		"tasks": []map[string]interface{}{
			{"id": "t1", "title": "Task 1", "description": "Do thing", "type": "code"},
		},
	})
	_, err := handleCreateTasks(context.Background(), q, input)
	if err != nil {
		t.Fatalf("create_tasks failed: %v", err)
	}

	result, err := handleGetStatus(context.Background(), q, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("get_status should work in dry-run mode, got error: %v", err)
	}
	if !strings.Contains(result.LLMContent, "t1") {
		t.Errorf("expected task t1 in status, got: %s", result.LLMContent)
	}
}

// TestDryRunCompleteShowsTaskGraph verifies complete prints the task graph in dry-run mode.
func TestDryRunCompleteShowsTaskGraph(t *testing.T) {
	q, _ := testQueen(t)
	q.cfg.Queen.DryRun = true

	// Create tasks with dependencies
	input := toJSON(map[string]interface{}{
		"tasks": []map[string]interface{}{
			{"id": "t1", "title": "Setup project", "description": "Init", "type": "code", "priority": 3},
			{"id": "t2", "title": "Write tests", "description": "Tests", "type": "test", "priority": 2, "depends_on": []string{"t1"}},
			{"id": "t3", "title": "Review", "description": "Review all", "type": "review", "priority": 1, "depends_on": []string{"t1", "t2"}},
		},
	})
	_, err := handleCreateTasks(context.Background(), q, input)
	if err != nil {
		t.Fatalf("create_tasks failed: %v", err)
	}

	result, err := handleComplete(context.Background(), q, toJSON(map[string]interface{}{"summary": "Plan complete"}))
	if err != nil {
		t.Fatalf("complete should work in dry-run mode, got error: %v", err)
	}

	// Verify the output contains task graph elements
	if !strings.Contains(result.LLMContent, "DRY-RUN TASK GRAPH") {
		t.Errorf("expected DRY-RUN TASK GRAPH header, got: %s", result.LLMContent)
	}
	if !strings.Contains(result.LLMContent, "Setup project") {
		t.Errorf("expected task title in graph, got: %s", result.LLMContent)
	}
	if !strings.Contains(result.LLMContent, "Write tests") {
		t.Errorf("expected task title in graph, got: %s", result.LLMContent)
	}
	if !strings.Contains(result.LLMContent, "Depends on: t1, t2") || !strings.Contains(result.LLMContent, "Depends on: t1\n") {
		// At least one dependency should be shown
		if !strings.Contains(result.LLMContent, "Depends on:") {
			t.Errorf("expected dependency info in graph, got: %s", result.LLMContent)
		}
	}
	if !strings.Contains(result.LLMContent, "critical") {
		t.Errorf("expected priority label in graph, got: %s", result.LLMContent)
	}
	if !strings.Contains(result.LLMContent, "No workers were executed") {
		t.Errorf("expected dry-run footer, got: %s", result.LLMContent)
	}
}

// TestDryRunAllowsCompleteAndFail verifies complete and fail work in dry-run mode.
func TestDryRunAllowsCompleteAndFail(t *testing.T) {
	q, _ := testQueen(t)
	q.cfg.Queen.DryRun = true

	// Test complete
	_, err := handleComplete(context.Background(), q, toJSON(map[string]interface{}{"summary": "Done"}))
	if err != nil {
		t.Fatalf("complete should work in dry-run mode, got: %v", err)
	}
	if q.phase != PhaseDone {
		t.Errorf("expected phase Done, got %s", q.phase)
	}
}

// TestDryRunPromptContainsInstruction verifies the system prompt includes dry-run instructions.
func TestDryRunPromptContainsInstruction(t *testing.T) {
	q, _ := testQueen(t)
	q.cfg.Queen.DryRun = true

	prompt := q.buildSystemPrompt()
	if !strings.Contains(prompt, "DRY-RUN MODE ACTIVE") {
		t.Error("system prompt should contain dry-run instruction")
	}
	if !strings.Contains(prompt, "Do NOT call assign_task") {
		t.Error("system prompt should warn against assign_task")
	}
}

// TestNonDryRunPromptOmitsInstruction verifies the system prompt does NOT include dry-run instructions normally.
func TestNonDryRunPromptOmitsInstruction(t *testing.T) {
	q, _ := testQueen(t)
	q.cfg.Queen.DryRun = false

	prompt := q.buildSystemPrompt()
	if strings.Contains(prompt, "DRY-RUN MODE ACTIVE") {
		t.Error("system prompt should NOT contain dry-run instruction when not in dry-run mode")
	}
}
