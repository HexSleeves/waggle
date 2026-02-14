package queen

import (
	"log"
	"os"
	"strings"
	"testing"

	"github.com/exedev/queen-bee/internal/blackboard"
	"github.com/exedev/queen-bee/internal/bus"
	"github.com/exedev/queen-bee/internal/task"
)

func newTestQueenForReplan(objective string) *Queen {
	msgBus := bus.New(100)
	return &Queen{
		objective: objective,
		board:     blackboard.New(msgBus),
		tasks:     task.NewTaskGraph(msgBus),
		logger:    log.New(os.Stderr, "test: ", 0),
	}
}

func TestBuildReplanPrompt_IncludesObjective(t *testing.T) {
	q := newTestQueenForReplan("Build a REST API for user management")

	prompt := q.buildReplanPrompt()

	if !strings.Contains(prompt, "Build a REST API for user management") {
		t.Error("prompt should contain the objective")
	}
	if !strings.Contains(prompt, "OBJECTIVE:") {
		t.Error("prompt should have OBJECTIVE header")
	}
}

func TestBuildReplanPrompt_IncludesCompletedTasks(t *testing.T) {
	q := newTestQueenForReplan("Refactor the database layer")

	completedTask := &task.Task{
		ID:     "task-1",
		Type:   task.TypeCode,
		Status: task.StatusComplete,
		Title:  "Create schema migration",
		Result: &task.Result{
			Success: true,
			Output:  "Created migration file db/001_users.sql",
		},
	}
	q.tasks.Add(completedTask)

	prompt := q.buildReplanPrompt()

	if !strings.Contains(prompt, "Create schema migration") {
		t.Error("prompt should contain completed task title")
	}
	if !strings.Contains(prompt, "task-1") {
		t.Error("prompt should contain completed task ID")
	}
	if !strings.Contains(prompt, "Created migration file") {
		t.Error("prompt should contain completed task output")
	}
}

func TestBuildReplanPrompt_IncludesFailedTasks(t *testing.T) {
	q := newTestQueenForReplan("Deploy the application")

	failedTask := &task.Task{
		ID:        "task-2",
		Type:      task.TypeGeneric,
		Status:    task.StatusFailed,
		Title:     "Run integration tests",
		LastError: "connection refused: database not running",
	}
	q.tasks.Add(failedTask)

	prompt := q.buildReplanPrompt()

	if !strings.Contains(prompt, "Run integration tests") {
		t.Error("prompt should contain failed task title")
	}
	if !strings.Contains(prompt, "task-2") {
		t.Error("prompt should contain failed task ID")
	}
	if !strings.Contains(prompt, "connection refused") {
		t.Error("prompt should contain the error message")
	}
}

func TestBuildReplanPrompt_IncludesPendingTasks(t *testing.T) {
	q := newTestQueenForReplan("Build feature X")

	q.tasks.Add(&task.Task{
		ID:     "task-3",
		Type:   task.TypeCode,
		Status: task.StatusPending,
		Title:  "Write unit tests",
	})

	prompt := q.buildReplanPrompt()

	if !strings.Contains(prompt, "Write unit tests") {
		t.Error("prompt should contain pending task title")
	}
	if !strings.Contains(prompt, "PENDING/READY TASKS") {
		t.Error("prompt should have PENDING/READY TASKS section")
	}
}

func TestBuildReplanPrompt_EmptyState(t *testing.T) {
	q := newTestQueenForReplan("Do something")

	prompt := q.buildReplanPrompt()

	if !strings.Contains(prompt, "(none)") {
		t.Error("prompt should show (none) when no tasks exist")
	}
	if !strings.Contains(prompt, "JSON array") {
		t.Error("prompt should contain JSON output instructions")
	}
}

func TestBuildReplanPrompt_AllSections(t *testing.T) {
	q := newTestQueenForReplan("Full integration")

	q.tasks.Add(&task.Task{
		ID: "c1", Type: task.TypeCode, Status: task.StatusComplete,
		Title: "Completed work", Result: &task.Result{Success: true, Output: "done"},
	})
	q.tasks.Add(&task.Task{
		ID: "f1", Type: task.TypeTest, Status: task.StatusFailed,
		Title: "Failed work", LastError: "timeout",
	})
	q.tasks.Add(&task.Task{
		ID: "p1", Type: task.TypeGeneric, Status: task.StatusPending,
		Title: "Pending work",
	})

	prompt := q.buildReplanPrompt()

	for _, expected := range []string{
		"OBJECTIVE: Full integration",
		"COMPLETED TASKS:",
		"Completed work",
		"FAILED TASKS:",
		"Failed work",
		"timeout",
		"PENDING/READY TASKS:",
		"Pending work",
		"INSTRUCTIONS:",
	} {
		if !strings.Contains(prompt, expected) {
			t.Errorf("prompt missing expected content: %q", expected)
		}
	}
}

func TestBuildReplanPrompt_FailedTaskErrorFromResult(t *testing.T) {
	q := newTestQueenForReplan("Test error sources")

	// Task with error only in Result.Errors (no LastError)
	q.tasks.Add(&task.Task{
		ID: "f2", Type: task.TypeCode, Status: task.StatusFailed,
		Title: "Compile code",
		Result: &task.Result{
			Success: false,
			Errors:  []string{"syntax error on line 42"},
		},
	})

	prompt := q.buildReplanPrompt()

	if !strings.Contains(prompt, "syntax error on line 42") {
		t.Error("prompt should include error from Result.Errors when LastError is empty")
	}
}

func TestTaskOutput_FromResult(t *testing.T) {
	q := newTestQueenForReplan("test")

	tk := &task.Task{
		ID:     "t1",
		Result: &task.Result{Success: true, Output: "hello from result"},
	}

	got := q.taskOutput(tk)
	if got != "hello from result" {
		t.Errorf("expected output from Result, got %q", got)
	}
}

func TestTaskOutput_FromBlackboard(t *testing.T) {
	q := newTestQueenForReplan("test")

	// Post to blackboard
	q.board.Post(&blackboard.Entry{
		Key:    "result-t2",
		Value:  "hello from blackboard",
		TaskID: "t2",
	})

	tk := &task.Task{ID: "t2"} // no Result

	got := q.taskOutput(tk)
	if got != "hello from blackboard" {
		t.Errorf("expected output from blackboard, got %q", got)
	}
}
