package queen

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/exedev/queen-bee/internal/config"
	"github.com/exedev/queen-bee/internal/errors"
	"github.com/exedev/queen-bee/internal/state"
	"github.com/exedev/queen-bee/internal/task"
)

// TestErrorClassification verifies that the queen properly classifies errors
// and handles retryable vs permanent errors correctly.
func TestErrorClassification(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "queen-error-class-test-*")
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
			Model:          "test-model",
			Provider:       "test",
			MaxIterations:  10,
			PlanTimeout:    5 * time.Minute,
			ReviewTimeout:  5 * time.Minute,
		},
		Workers: config.WorkerConfig{
			MaxParallel:    2,
			MaxRetries:     3,
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

	// Create a test task
	testTask := &task.Task{
		ID:         "error-test-task",
		Type:       task.TypeGeneric,
		Status:     task.StatusPending,
		Priority:   task.PriorityNormal,
		Title:      "Error Classification Test Task",
		MaxRetries: 3,
		RetryCount: 0,
	}

	q.tasks.Add(testTask)
	q.db.InsertTask(context.Background(), q.sessionID, state.TaskRow{
		ID:         testTask.ID,
		Type:       string(testTask.Type),
		Status:     string(testTask.Status),
		Priority:   int(testTask.Priority),
		Title:      testTask.Title,
		MaxRetries: testTask.MaxRetries,
		RetryCount: testTask.RetryCount,
	})

	// Test 1: Retryable error (network timeout)
	t.Run("RetryableError", func(t *testing.T) {
		retryableResult := &task.Result{
			Success: false,
			Errors:  []string{"[retryable] connection timeout"},
		}

		// Reset task state
		testTask.RetryCount = 0
		testTask.Status = task.StatusRunning
		q.tasks.UpdateStatus(testTask.ID, task.StatusRunning)

		ctx := make(map[string]interface{})
		_ = ctx

		q.handleTaskFailure(context.Background(), testTask.ID, "worker-1", retryableResult)

		// Task should be pending for retry
		taskAfter, ok := q.tasks.Get(testTask.ID)
		if !ok {
			t.Fatal("Task not found after failure")
		}

		if taskAfter.Status != task.StatusPending {
			t.Errorf("Expected task to be pending for retry, got %s", taskAfter.Status)
		}

		if taskAfter.RetryCount != 1 {
			t.Errorf("Expected retry count to be 1, got %d", taskAfter.RetryCount)
		}

		if taskAfter.LastErrorType != string(errors.ErrorTypeRetryable) {
			t.Errorf("Expected error type to be %s, got %s", errors.ErrorTypeRetryable, taskAfter.LastErrorType)
		}
	})

	// Test 2: Permanent error (command not found)
	t.Run("PermanentError", func(t *testing.T) {
		permanentResult := &task.Result{
			Success: false,
			Errors:  []string{"command not found: unknown-cmd"},
		}

		// Reset task state
		testTask.RetryCount = 0
		testTask.Status = task.StatusRunning
		q.tasks.UpdateStatus(testTask.ID, task.StatusRunning)

		q.handleTaskFailure(context.Background(), testTask.ID, "worker-2", permanentResult)

		// Task should be failed immediately (no retries for permanent errors)
		taskAfter, ok := q.tasks.Get(testTask.ID)
		if !ok {
			t.Fatal("Task not found after failure")
		}

		if taskAfter.Status != task.StatusFailed {
			t.Errorf("Expected task to be failed immediately for permanent error, got %s", taskAfter.Status)
		}

		// Retry count should not be incremented for permanent errors
		if taskAfter.RetryCount != 0 {
			t.Errorf("Expected retry count to remain 0 for permanent error, got %d", taskAfter.RetryCount)
		}

		if taskAfter.LastErrorType != string(errors.ErrorTypePermanent) {
			t.Errorf("Expected error type to be %s, got %s", errors.ErrorTypePermanent, taskAfter.LastErrorType)
		}
	})

	// Test 3: Panic error
	t.Run("PanicError", func(t *testing.T) {
		panicResult := &task.Result{
			Success: false,
			Errors:  []string{"panic: runtime error: index out of range"},
		}

		// Reset task state
		testTask.RetryCount = 0
		testTask.Status = task.StatusRunning
		q.tasks.UpdateStatus(testTask.ID, task.StatusRunning)

		q.handleTaskFailure(context.Background(), testTask.ID, "worker-3", panicResult)

		// Task should be pending for retry (panics are treated as retryable by default)
		taskAfter, ok := q.tasks.Get(testTask.ID)
		if !ok {
			t.Fatal("Task not found after failure")
		}

		if taskAfter.Status != task.StatusPending {
			t.Errorf("Expected task to be pending for retry after panic, got %s", taskAfter.Status)
		}
	})

	// Test 4: Max retries exceeded
	t.Run("MaxRetriesExceeded", func(t *testing.T) {
		retryableResult := &task.Result{
			Success: false,
			Errors:  []string{"[retryable] connection timeout"},
		}

		// Set retry count at max
		testTask.RetryCount = 3
		testTask.Status = task.StatusRunning
		q.tasks.UpdateStatus(testTask.ID, task.StatusRunning)

		q.handleTaskFailure(context.Background(), testTask.ID, "worker-4", retryableResult)

		// Task should be failed after max retries
		taskAfter, ok := q.tasks.Get(testTask.ID)
		if !ok {
			t.Fatal("Task not found after failure")
		}

		if taskAfter.Status != task.StatusFailed {
			t.Errorf("Expected task to be failed after max retries, got %s", taskAfter.Status)
		}
	})
}

// TestErrorClassificationPatterns tests various error patterns
func TestErrorClassificationPatterns(t *testing.T) {
	tests := []struct {
		name         string
		errMsg       string
		expectRetry  bool
		expectedType errors.ErrorType
	}{
		// Retryable patterns
		{"network timeout", "connection timeout", true, errors.ErrorTypeRetryable},
		{"rate limit", "rate limit exceeded, try again later", true, errors.ErrorTypeRetryable},
		{"503 error", "503 service unavailable", true, errors.ErrorTypeRetryable},
		{"gateway timeout", "504 gateway timeout", true, errors.ErrorTypeRetryable},
		{"connection refused", "connection refused", true, errors.ErrorTypeRetryable},

		// Permanent patterns
		{"command not found", "command not found", false, errors.ErrorTypePermanent},
		{"file not found", "no such file or directory", false, errors.ErrorTypePermanent},
		{"permission denied", "permission denied", false, errors.ErrorTypePermanent},
		{"invalid argument", "invalid argument", false, errors.ErrorTypePermanent},
		{"401 unauthorized", "401 unauthorized", false, errors.ErrorTypePermanent},
		{"403 forbidden", "403 forbidden", false, errors.ErrorTypePermanent},
		{"404 not found", "404 not found", false, errors.ErrorTypePermanent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := fmt.Errorf("%s", tt.errMsg)
			errType := errors.ClassifyError(err)
			isRetryable := errors.IsRetryable(err)

			if errType != tt.expectedType {
				t.Errorf("ClassifyError() = %v, want %v", errType, tt.expectedType)
			}

			if isRetryable != tt.expectRetry {
				t.Errorf("IsRetryable() = %v, want %v", isRetryable, tt.expectRetry)
			}
		})
	}
}

// TestExponentialBackoff tests the backoff calculation
func TestExponentialBackoff(t *testing.T) {
	tests := []struct {
		name       string
		retryCount int
		expected   time.Duration
	}{
		{"0 retries", 0, 2 * time.Second},
		{"1 retry", 1, 4 * time.Second},
		{"2 retries", 2, 8 * time.Second},
		{"3 retries", 3, 16 * time.Second},
		{"4 retries", 4, 32 * time.Second},
		{"5 retries (capped)", 5, 60 * time.Second},
	}

	baseDelay := 2 * time.Second
	maxDelay := 60 * time.Second

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delay := errors.CalculateBackoff(baseDelay, tt.retryCount, maxDelay)
			if delay != tt.expected {
				t.Errorf("CalculateBackoff() = %v, want %v", delay, tt.expected)
			}
		})
	}
}
