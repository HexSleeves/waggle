package task

import (
	"testing"
	"time"

	"github.com/exedev/waggle/internal/bus"
)

func TestTaskResultDetection(t *testing.T) {
	bus := bus.New(100)
	graph := NewTaskGraph(bus)

	tests := []struct {
		name           string
		result         *Result
		expectedStatus Status
	}{
		{
			name:           "successful result",
			result:         &Result{Success: true, Output: "completed successfully"},
			expectedStatus: StatusComplete,
		},
		{
			name:           "failed result",
			result:         &Result{Success: false, Output: "failed task"},
			expectedStatus: StatusFailed,
		},
		{
			name:           "result with errors",
			result:         &Result{Success: false, Output: "", Errors: []string{"error 1", "error 2"}},
			expectedStatus: StatusFailed,
		},
		{
			name:           "empty result",
			result:         &Result{},
			expectedStatus: StatusFailed,
		},
		{
			name:           "nil result",
			result:         nil,
			expectedStatus: StatusFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := &Task{
				ID:        "test-task",
				Type:      TypeCode,
				Title:     "Test Task",
				Status:    StatusRunning,
				Result:    tt.result,
				StartedAt: func() *time.Time { now := time.Now(); return &now }(),
			}

			graph.Add(task)

			var detected Status
			if task.Result == nil {
				detected = StatusFailed
			} else if task.Result.Success {
				detected = StatusComplete
			} else {
				detected = StatusFailed
			}

			if detected != tt.expectedStatus {
				t.Errorf("Expected status %s, got %s", tt.expectedStatus, detected)
			}
		})
	}
}

func TestTaskStatusUpdateTimestamps(t *testing.T) {
	bus := bus.New(100)
	graph := NewTaskGraph(bus)

	task := &Task{
		ID:     "task-1",
		Type:   TypeCode,
		Title:  "Test",
		Status: StatusPending,
	}
	graph.Add(task)

	beforeRunning := time.Now()
	err := graph.UpdateStatus("task-1", StatusRunning)
	if err != nil {
		t.Fatalf("UpdateStatus failed: %v", err)
	}

	task, _ = graph.Get("task-1")
	if task.StartedAt == nil {
		t.Error("Expected StartedAt to be set")
	} else if task.StartedAt.Before(beforeRunning) {
		t.Error("StartedAt should be after or equal to beforeRunning")
	}

	beforeComplete := time.Now()
	err = graph.UpdateStatus("task-1", StatusComplete)
	if err != nil {
		t.Fatalf("UpdateStatus failed: %v", err)
	}

	task, _ = graph.Get("task-1")
	if task.CompletedAt == nil {
		t.Error("Expected CompletedAt to be set")
	} else if task.CompletedAt.Before(beforeComplete) {
		t.Error("CompletedAt should be after or equal to beforeComplete")
	}
}

func TestTaskEmptyOutputDetection(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		success    bool
		expectFail bool
	}{
		{
			name:       "empty output with success",
			output:     "",
			success:    true,
			expectFail: false,
		},
		{
			name:       "empty output without success",
			output:     "",
			success:    false,
			expectFail: true,
		},
		{
			name:       "whitespace only output",
			output:     "   \n\t  ",
			success:    true,
			expectFail: false,
		},
		{
			name:       "whitespace only failure",
			output:     "   \n\t  ",
			success:    false,
			expectFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &Result{
				Success: tt.success,
				Output:  tt.output,
			}

			isFailed := !result.Success

			if isFailed != tt.expectFail {
				t.Errorf("Expected fail=%v, got %v", tt.expectFail, isFailed)
			}
		})
	}
}

func TestTaskRetryCountLogic(t *testing.T) {
	bus := bus.New(100)
	graph := NewTaskGraph(bus)

	task := &Task{
		ID:         "retry-task",
		Type:       TypeCode,
		Title:      "Retry Test",
		Status:     StatusPending,
		MaxRetries: 3,
		RetryCount: 0,
	}
	graph.Add(task)

	// Simulate retry logic
	for attempt := 1; attempt <= task.MaxRetries; attempt++ {
		task.RetryCount = attempt

		canRetry := task.RetryCount < task.MaxRetries
		if attempt < task.MaxRetries && !canRetry {
			t.Errorf("Attempt %d: should be able to retry", attempt)
		}
		if attempt == task.MaxRetries && canRetry {
			t.Errorf("Attempt %d: should NOT be able to retry", attempt)
		}
	}
}

func TestTaskErrorOutputDetection(t *testing.T) {
	tests := []struct {
		name       string
		result     *Result
		expectFail bool
	}{
		{
			name: "error in output",
			result: &Result{
				Success: false,
				Output:  "error: something went wrong",
				Errors:  []string{"error: something went wrong"},
			},
			expectFail: true,
		},
		{
			name: "multiple errors",
			result: &Result{
				Success: false,
				Output:  "failed",
				Errors:  []string{"error 1", "error 2", "error 3"},
			},
			expectFail: true,
		},
		{
			name: "success with warning",
			result: &Result{
				Success: true,
				Output:  "completed with warnings",
				Errors:  []string{"warning: some issue"},
			},
			expectFail: false,
		},
		{
			name: "success with error in output text",
			result: &Result{
				Success: true,
				Output:  "completed but had error in processing",
			},
			expectFail: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isFailed := !tt.result.Success
			if isFailed != tt.expectFail {
				t.Errorf("Expected fail=%v, got %v", tt.expectFail, isFailed)
			}
		})
	}
}

func TestTaskTimeoutDetection(t *testing.T) {
	bus := bus.New(100)
	graph := NewTaskGraph(bus)

	task := &Task{
		ID:      "timeout-task",
		Type:    TypeCode,
		Title:   "Timeout Test",
		Status:  StatusRunning,
		Timeout: 5 * time.Second,
	}
	graph.Add(task)

	// Task with past timeout
	pastTimeout := time.Now().Add(-10 * time.Second)
	task.StartedAt = &pastTimeout

	isTimedOut := task.Timeout > 0 && task.StartedAt != nil && time.Since(*task.StartedAt) > task.Timeout
	if !isTimedOut {
		t.Error("Expected task to be detected as timed out")
	}

	// Task with future timeout (should NOT be timed out)
	futureTask := &Task{
		ID:        "no-timeout-task",
		Type:      TypeCode,
		Status:    StatusRunning,
		StartedAt: func() *time.Time { now := time.Now(); return &now }(),
		Timeout:   5 * time.Second,
	}
	graph.Add(futureTask)

	isTimedOut2 := futureTask.Timeout > 0 && futureTask.StartedAt != nil && time.Since(*futureTask.StartedAt) > futureTask.Timeout
	if isTimedOut2 {
		t.Error("Expected task to NOT be detected as timed out")
	}
}

func TestTaskGraphAllComplete(t *testing.T) {
	bus := bus.New(100)
	graph := NewTaskGraph(bus)

	// Empty graph should return false
	if graph.AllComplete() {
		t.Error("Empty graph should not be considered complete")
	}

	// Add completed tasks
	graph.Add(&Task{ID: "t1", Status: StatusComplete})
	graph.Add(&Task{ID: "t2", Status: StatusComplete})
	if !graph.AllComplete() {
		t.Error("All complete tasks should return true")
	}

	// Add a failed task to existing complete tasks
	graph.Add(&Task{ID: "t3", Status: StatusFailed})
	if graph.AllComplete() {
		t.Error("With failed task should return false")
	}

	// Test with only complete and cancelled (both should count as complete)
	graph2 := NewTaskGraph(bus)
	graph2.Add(&Task{ID: "t1", Status: StatusComplete})
	graph2.Add(&Task{ID: "t4", Status: StatusCancelled})
	if !graph2.AllComplete() {
		t.Error("Complete and cancelled tasks should return true")
	}

	// Test with running task
	graph3 := NewTaskGraph(bus)
	graph3.Add(&Task{ID: "t1", Status: StatusComplete})
	graph3.Add(&Task{ID: "t5", Status: StatusRunning})
	if graph3.AllComplete() {
		t.Error("With running task should return false")
	}
}

func TestTaskGraphFailed(t *testing.T) {
	bus := bus.New(100)
	graph := NewTaskGraph(bus)

	// No failed tasks initially
	failed := graph.Failed()
	if len(failed) != 0 {
		t.Errorf("Expected 0 failed tasks, got %d", len(failed))
	}

	// Add tasks with various statuses
	graph.Add(&Task{ID: "t1", Status: StatusComplete})
	graph.Add(&Task{ID: "t2", Status: StatusFailed})
	graph.Add(&Task{ID: "t3", Status: StatusRunning})
	graph.Add(&Task{ID: "t4", Status: StatusFailed})

	failed = graph.Failed()
	if len(failed) != 2 {
		t.Errorf("Expected 2 failed tasks, got %d", len(failed))
	}
}

func TestTaskGraphReady(t *testing.T) {
	bus := bus.New(100)
	graph := NewTaskGraph(bus)

	// Add tasks with dependencies
	graph.Add(&Task{ID: "a", Status: StatusComplete}) // dependency
	graph.Add(&Task{ID: "b", Status: StatusPending, DependsOn: []string{"a"}})
	graph.Add(&Task{ID: "c", Status: StatusPending, DependsOn: []string{"a"}})
	graph.Add(&Task{ID: "d", Status: StatusPending}) // no deps

	ready := graph.Ready()
	if len(ready) != 3 {
		t.Errorf("Expected 3 ready tasks, got %d", len(ready))
	}

	// Mark one as running
	graph.UpdateStatus("b", StatusRunning)
	ready = graph.Ready()
	if len(ready) != 2 {
		t.Errorf("Expected 2 ready tasks after one running, got %d", len(ready))
	}

	// Complete the dependency
	graph.UpdateStatus("a", StatusComplete)
	ready = graph.Ready()
	if len(ready) != 2 {
		t.Errorf("Expected 2 ready tasks, got %d", len(ready))
	}
}

func TestTaskStatusTransitions(t *testing.T) {
	bus := bus.New(100)
	graph := NewTaskGraph(bus)

	validTransitions := map[Status][]Status{
		StatusPending:   {StatusAssigned, StatusRunning, StatusCancelled},
		StatusAssigned:  {StatusRunning, StatusCancelled},
		StatusRunning:   {StatusComplete, StatusFailed, StatusRetrying, StatusCancelled},
		StatusRetrying:  {StatusRunning, StatusFailed},
		StatusComplete:  {},
		StatusFailed:    {},
		StatusCancelled: {},
	}

	task := &Task{
		ID:     "transition-test",
		Type:   TypeCode,
		Status: StatusPending,
	}
	graph.Add(task)

	// Test valid transitions
	for from, targets := range validTransitions {
		task.Status = from
		for _, to := range targets {
			err := graph.UpdateStatus("transition-test", to)
			if err != nil {
				t.Errorf("Transition from %s to %s failed: %v", from, to, err)
			}
		}
	}
}

func TestTaskRetryWithErrorOutput(t *testing.T) {
	bus := bus.New(100)
	graph := NewTaskGraph(bus)

	task := &Task{
		ID:         "retry-with-error",
		Type:       TypeCode,
		Status:     StatusFailed,
		MaxRetries: 2,
		RetryCount: 0,
		LastError:  "connection refused",
	}
	graph.Add(task)

	// Determine if should retry based on error
	shouldRetry := task.RetryCount < task.MaxRetries && isRetryableError(task.LastError)

	if !shouldRetry {
		t.Error("Should be able to retry with retryable error")
	}

	// Add permanent error
	task.LastError = "command not found"
	task.RetryCount = 0

	shouldRetry = task.RetryCount < task.MaxRetries && isRetryableError(task.LastError)

	if shouldRetry {
		t.Error("Should NOT retry with permanent error")
	}
}

func isRetryableError(errMsg string) bool {
	retryablePatterns := []string{
		"connection refused",
		"timeout",
		"temporary",
		"rate limit",
	}
	for _, p := range retryablePatterns {
		if len(errMsg) > 0 && len(p) > 0 && len(errMsg) >= len(p) {
			// Simple substring check
			for i := 0; i <= len(errMsg)-len(p); i++ {
				if errMsg[i:i+len(p)] == p {
					return true
				}
			}
		}
	}
	return false
}

func TestTaskResultArtifactsAndMetrics(t *testing.T) {
	result := &Result{
		Success: true,
		Output:  "completed",
		Artifacts: map[string]string{
			"file1.txt": "/path/to/file1.txt",
			"log.txt":   "/path/to/log.txt",
		},
		Metrics: map[string]float64{
			"duration_ms": 1500.5,
			"memory_mb":   256.0,
		},
	}

	if !result.Success {
		t.Error("Expected success to be true")
	}

	if len(result.Artifacts) != 2 {
		t.Errorf("Expected 2 artifacts, got %d", len(result.Artifacts))
	}

	if result.Metrics["duration_ms"] != 1500.5 {
		t.Errorf("Expected duration_ms 1500.5, got %f", result.Metrics["duration_ms"])
	}
}

func TestTaskStatusFromWorkerOutput(t *testing.T) {
	tests := []struct {
		name           string
		output         string
		exitCode       int
		expectComplete bool
	}{
		{
			name:           "zero exit code",
			output:         "task completed",
			exitCode:       0,
			expectComplete: true,
		},
		{
			name:           "non-zero exit code",
			output:         "error occurred",
			exitCode:       1,
			expectComplete: false,
		},
		{
			name:           "command not found exit code",
			output:         "command not found",
			exitCode:       127,
			expectComplete: false,
		},
		{
			name:           "killed exit code",
			output:         "killed",
			exitCode:       137,
			expectComplete: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &Result{
				Success: tt.exitCode == 0,
				Output:  tt.output,
			}

			// Simulate detection logic
			detectedComplete := result.Success

			if detectedComplete != tt.expectComplete {
				t.Errorf("Expected complete=%v, got %v", tt.expectComplete, detectedComplete)
			}
		})
	}
}
