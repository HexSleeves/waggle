package worker

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/exedev/waggle/internal/bus"
	"github.com/exedev/waggle/internal/task"
)

// mockBeeWithOutput is a test implementation that can simulate various output scenarios
type mockBeeWithOutput struct {
	id           string
	beeType      string
	status       atomic.Value
	result       *task.Result
	spawnErr     error
	killCalled   atomic.Bool
	mu           sync.Mutex
	output       string
	completeChan chan struct{}
}

func newMockBeeWithOutput(id, beeType string) *mockBeeWithOutput {
	m := &mockBeeWithOutput{
		id:           id,
		beeType:      beeType,
		result:       nil,
		output:       "",
		completeChan: make(chan struct{}),
	}
	m.status.Store(StatusIdle)
	return m
}

func (m *mockBeeWithOutput) ID() string {
	return m.id
}

func (m *mockBeeWithOutput) Type() string {
	return m.beeType
}

func (m *mockBeeWithOutput) Spawn(ctx context.Context, t *task.Task) error {
	if m.spawnErr != nil {
		return m.spawnErr
	}
	m.status.Store(StatusRunning)

	go func() {
		select {
		case <-ctx.Done():
			m.mu.Lock()
			m.status.Store(StatusFailed)
			m.result = &task.Result{Success: false, Output: "context cancelled"}
			m.mu.Unlock()
		case <-m.completeChan:
		}
	}()

	return nil
}

func (m *mockBeeWithOutput) Monitor() Status {
	status, ok := m.status.Load().(Status)
	if !ok {
		return StatusIdle
	}
	return status
}

func (m *mockBeeWithOutput) Result() *task.Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.result
}

func (m *mockBeeWithOutput) Kill() error {
	m.killCalled.Store(true)
	m.status.Store(StatusFailed)
	close(m.completeChan)
	return nil
}

func (m *mockBeeWithOutput) Output() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.output
}

func (m *mockBeeWithOutput) completeWithSuccess(output string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Store(StatusComplete)
	m.result = &task.Result{Success: true, Output: output}
	close(m.completeChan)
}

func (m *mockBeeWithOutput) completeWithFailure(output string, errs []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Store(StatusFailed)
	m.result = &task.Result{Success: false, Output: output, Errors: errs}
	close(m.completeChan)
}

func (m *mockBeeWithOutput) completeWithEmptyOutput() {
	m.completeWithSuccess("")
}

func TestWorkerStatusDetection(t *testing.T) {
	tests := []struct {
		name          string
		status        Status
		expectedState string
	}{
		{"idle", StatusIdle, "idle"},
		{"running", StatusRunning, "running"},
		{"stuck", StatusStuck, "stuck"},
		{"complete", StatusComplete, "complete"},
		{"failed", StatusFailed, "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bee := newMockBeeWithOutput("test-worker", "test")
			bee.status.Store(tt.status)

			if string(bee.Monitor()) != tt.expectedState {
				t.Errorf("Expected %s, got %s", tt.expectedState, bee.Monitor())
			}
		})
	}
}

func TestWorkerResultDetection(t *testing.T) {
	tests := []struct {
		name           string
		result         *task.Result
		expectSuccess  bool
		expectComplete bool
	}{
		{
			name:           "successful result",
			result:         &task.Result{Success: true, Output: "completed"},
			expectSuccess:  true,
			expectComplete: true,
		},
		{
			name:           "failed result",
			result:         &task.Result{Success: false, Output: "failed"},
			expectSuccess:  false,
			expectComplete: true,
		},
		{
			name:           "nil result while running",
			result:         nil,
			expectSuccess:  false,
			expectComplete: false,
		},
		{
			name:           "result with multiple errors",
			result:         &task.Result{Success: false, Errors: []string{"err1", "err2", "err3"}},
			expectSuccess:  false,
			expectComplete: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bee := newMockBeeWithOutput("test-worker", "test")
			bee.result = tt.result

			result := bee.Result()
			if result == nil {
				if tt.expectComplete {
					t.Error("Expected result but got nil")
				}
			} else {
				if result.Success != tt.expectSuccess {
					t.Errorf("Expected success=%v, got %v", tt.expectSuccess, result.Success)
				}
			}
		})
	}
}

func TestWorkerEmptyOutputDetection(t *testing.T) {
	tests := []struct {
		name             string
		output           string
		success          bool
		detectAsComplete bool
	}{
		{
			name:             "empty output with success",
			output:           "",
			success:          true,
			detectAsComplete: true,
		},
		{
			name:             "empty output with failure",
			output:           "",
			success:          false,
			detectAsComplete: false,
		},
		{
			name:             "whitespace only output",
			output:           "   \n\t  ",
			success:          true,
			detectAsComplete: true,
		},
		{
			name:             "whitespace with failure",
			output:           "   \n\t  ",
			success:          false,
			detectAsComplete: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bee := newMockBeeWithOutput("test-worker", "test")
			bee.result = &task.Result{Success: tt.success, Output: tt.output}

			result := bee.Result()
			detectComplete := result != nil && result.Success

			if detectComplete != tt.detectAsComplete {
				t.Errorf("Expected detectAsComplete=%v, got %v", tt.detectAsComplete, detectComplete)
			}
		})
	}
}

func TestWorkerErrorOutputDetection(t *testing.T) {
	tests := []struct {
		name       string
		result     *task.Result
		expectFail bool
	}{
		{
			name: "error in output",
			result: &task.Result{
				Success: false,
				Output:  "error: something went wrong",
				Errors:  []string{"error: something went wrong"},
			},
			expectFail: true,
		},
		{
			name: "multiple errors",
			result: &task.Result{
				Success: false,
				Errors:  []string{"error 1", "error 2"},
			},
			expectFail: true,
		},
		{
			name: "success with warning",
			result: &task.Result{
				Success: true,
				Errors:  []string{"warning: some issue"},
			},
			expectFail: false,
		},
		{
			name:       "result nil",
			result:     nil,
			expectFail: false, // nil result while worker is idle/running is not a failure state
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bee := newMockBeeWithOutput("test-worker", "test")
			bee.result = tt.result

			isFailed := bee.Monitor() == StatusFailed || (bee.Result() != nil && !bee.Result().Success)

			if isFailed != tt.expectFail {
				t.Errorf("Expected fail=%v, got %v", tt.expectFail, isFailed)
			}
		})
	}

}

func isRetryableError(result *task.Result) bool {
	if result == nil || result.Success {
		return false
	}

	// Check for permanent errors first
	permanentPatterns := []string{
		"command not found",
		"executable file not found",
		"no such file or directory",
		"permission denied",
		"invalid argument",
		"bad request",
		"invalid syntax",
		"parse error",
		"not found",
		"404",
		"unauthorized",
		"forbidden",
		"401",
		"403",
	}

	if result.Errors != nil {
		for _, err := range result.Errors {
			errLower := strings.ToLower(err)
			for _, pattern := range permanentPatterns {
				if strings.Contains(errLower, strings.ToLower(pattern)) {
					return false // Permanent error
				}
			}
		}
	}

	// Check for retryable errors
	retryablePatterns := []string{
		"timeout",
		"connection refused",
		"temporary",
		"rate limit",
		"429",
		"503",
		"502",
		"504",
		"network",
		"retry",
	}

	if result.Errors != nil {
		for _, err := range result.Errors {
			errLower := strings.ToLower(err)
			for _, pattern := range retryablePatterns {
				if strings.Contains(errLower, strings.ToLower(pattern)) {
					return true
				}
			}
		}
	}

	// Default: unknown error is retryable
	return true
}

func TestWorkerTaskCompletionFromWorker(t *testing.T) {
	tests := []struct {
		name           string
		workerStatus   Status
		resultSuccess  bool
		expectComplete bool
		expectSuccess  bool
	}{
		{
			name:           "complete successful",
			workerStatus:   StatusComplete,
			resultSuccess:  true,
			expectComplete: true,
			expectSuccess:  true,
		},
		{
			name:           "complete failed",
			workerStatus:   StatusComplete,
			resultSuccess:  false,
			expectComplete: true,
			expectSuccess:  false,
		},
		{
			name:           "failed",
			workerStatus:   StatusFailed,
			resultSuccess:  false,
			expectComplete: true,
			expectSuccess:  false,
		},
		{
			name:           "running - no result yet",
			workerStatus:   StatusRunning,
			resultSuccess:  false,
			expectComplete: false,
			expectSuccess:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bee := newMockBeeWithOutput("test-worker", "test")
			bee.status.Store(tt.workerStatus)
			bee.result = &task.Result{Success: tt.resultSuccess}

			status := bee.Monitor()
			result := bee.Result()

			isComplete := status == StatusComplete || status == StatusFailed

			if isComplete != tt.expectComplete {
				t.Errorf("Expected complete=%v, got %v", tt.expectComplete, isComplete)
			}

			if result != nil && result.Success != tt.expectSuccess {
				t.Errorf("Expected success=%v, got %v", tt.expectSuccess, result.Success)
			}
		})
	}
}

func TestWorkerPoolStatusAggregation(t *testing.T) {
	factory := func(id, adapter string) (Bee, error) {
		return newMockBeeWithOutput(id, adapter), nil
	}
	pool := NewPool(10, factory, nil)

	// Add workers in different states
	workers := []struct {
		id     string
		status Status
	}{
		{"w1", StatusRunning},
		{"w2", StatusRunning},
		{"w3", StatusComplete},
		{"w4", StatusFailed},
		{"w5", StatusIdle},
	}

	pool.mu.Lock()
	for _, w := range workers {
		bee := newMockBeeWithOutput(w.id, "test")
		bee.status.Store(w.status)
		pool.workers[w.id] = bee
	}
	pool.mu.Unlock()

	// Test Active() returns only running workers
	active := pool.Active()
	if len(active) != 2 {
		t.Errorf("Expected 2 active workers, got %d", len(active))
	}

	// Test ActiveCount()
	if pool.ActiveCount() != 2 {
		t.Errorf("Expected active count 2, got %d", pool.ActiveCount())
	}
}

func TestWorkerResultWithArtifacts(t *testing.T) {
	bee := newMockBeeWithOutput("artifacts-worker", "test")
	bee.result = &task.Result{
		Success: true,
		Output:  "Task completed",
		Artifacts: map[string]string{
			"output.txt": "/tmp/output.txt",
			"log.txt":    "/tmp/log.txt",
		},
		Metrics: map[string]float64{
			"duration_ms": 1500.0,
			"memory_mb":   256.5,
		},
	}

	result := bee.Result()
	if result == nil {
		t.Fatal("Expected result")
	}

	if !result.Success {
		t.Error("Expected success=true")
	}

	if len(result.Artifacts) != 2 {
		t.Errorf("Expected 2 artifacts, got %d", len(result.Artifacts))
	}

	if result.Metrics["duration_ms"] != 1500.0 {
		t.Errorf("Expected duration_ms=1500.0, got %f", result.Metrics["duration_ms"])
	}
}

func TestWorkerKillReleasesResources(t *testing.T) {
	bee := newMockBeeWithOutput("kill-test", "test")
	bee.status.Store(StatusRunning)

	err := bee.Kill()
	if err != nil {
		t.Errorf("Kill failed: %v", err)
	}

	if !bee.killCalled.Load() {
		t.Error("Kill was not called")
	}

	if bee.Monitor() != StatusFailed {
		t.Errorf("Expected status failed after kill, got %s", bee.Monitor())
	}
}

func TestWorkerStatusTransitionRace(t *testing.T) {
	factory := func(id, adapter string) (Bee, error) {
		return newMockBeeWithOutput(id, adapter), nil
	}
	pool := NewPool(10, factory, nil)

	var wg sync.WaitGroup

	// Concurrent status transitions
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			bee := newMockBeeWithOutput("race-worker", "test")

			pool.mu.Lock()
			pool.workers[bee.ID()] = bee
			pool.mu.Unlock()

			// Rapid status changes
			statuses := []Status{StatusIdle, StatusRunning, StatusComplete, StatusFailed}
			for _, s := range statuses {
				bee.status.Store(s)
				_ = bee.Monitor()
				_ = bee.Result()
			}
		}(i)
	}

	wg.Wait()
}

func TestWorkerResultErrorMessageParsing(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		expectRetry bool
	}{
		{
			name:        "connection refused",
			output:      "error: connection refused",
			expectRetry: true,
		},
		{
			name:        "command not found",
			output:      "error: command not found",
			expectRetry: false, // permanent error
		},
		{
			name:        "timeout error",
			output:      "error: timeout after 30s",
			expectRetry: true,
		},
		{
			name:        "file not found",
			output:      "error: file not found",
			expectRetry: false, // permanent error
		},
		{
			name:        "rate limit",
			output:      "error: rate limit exceeded",
			expectRetry: true,
		},
		{
			name:        "permission denied",
			output:      "error: permission denied",
			expectRetry: false, // permanent error
		},
		{
			name:        "generic error - default to retryable",
			output:      "error: something went wrong",
			expectRetry: true, // unknown errors default to retryable
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &task.Result{
				Success: false,
				Output:  tt.output,
				Errors:  []string{tt.output},
			}

			retryable := isRetryableError(result)

			if retryable != tt.expectRetry {
				t.Errorf("Expected retryable=%v, got %v (output: %s)", tt.expectRetry, retryable, tt.output)
			}
		})
	}
}

func TestBusPublishOnTaskStatusChange(t *testing.T) {
	b := bus.New(100)
	var receivedMsg *bus.Message
	var mu sync.Mutex

	b.Subscribe(bus.MsgTaskStatusChanged, func(msg bus.Message) {
		mu.Lock()
		defer mu.Unlock()
		receivedMsg = &msg
	})

	// Simulate task status change
	b.Publish(bus.Message{
		Type:   bus.MsgTaskStatusChanged,
		TaskID: "test-task",
		Time:   time.Now(),
	})

	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	if receivedMsg == nil {
		t.Error("Expected message to be published")
	} else if receivedMsg.Type != bus.MsgTaskStatusChanged {
		t.Errorf("Expected type %s, got %s", bus.MsgTaskStatusChanged, receivedMsg.Type)
	}
	mu.Unlock()
}

// Test that the test file compiles and can be run
func TestWorkerStatusDetectionTestSuite(t *testing.T) {
	t.Log("Worker status detection test suite ready")
}
