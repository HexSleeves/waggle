package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/exedev/waggle/internal/bus"
	"github.com/exedev/waggle/internal/task"
)

// mockBee is a test implementation of the Bee interface
type mockBee struct {
	id         string
	beeType    string
	status     atomic.Value
	result     *task.Result
	spawnErr   error
	spawnFunc  func(ctx context.Context, t *task.Task) error // custom spawn behavior
	killCalled atomic.Bool
	mu         sync.Mutex
	output     string
}

func newMockBee(id, beeType string) *mockBee {
	m := &mockBee{
		id:      id,
		beeType: beeType,
		result:  nil,
		output:  "",
	}
	m.status.Store(StatusIdle)
	return m
}

func (m *mockBee) ID() string {
	return m.id
}

func (m *mockBee) Type() string {
	return m.beeType
}

func (m *mockBee) Spawn(ctx context.Context, t *task.Task) error {
	if m.spawnErr != nil {
		return m.spawnErr
	}
	if m.spawnFunc != nil {
		return m.spawnFunc(ctx, t)
	}
	m.status.Store(StatusRunning)

	// Simulate async work completion
	go func() {
		time.Sleep(10 * time.Millisecond)
		m.mu.Lock()
		if m.status.Load() == StatusRunning {
			m.status.Store(StatusComplete)
			m.result = &task.Result{Success: true, Output: "completed"}
		}
		m.mu.Unlock()
	}()

	return nil
}

func (m *mockBee) Monitor() Status {
	status, ok := m.status.Load().(Status)
	if !ok {
		return StatusIdle
	}
	return status
}

func (m *mockBee) Result() *task.Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.result
}

func (m *mockBee) Kill() error {
	m.killCalled.Store(true)
	m.status.Store(StatusFailed)
	return nil
}

func (m *mockBee) Output() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.output
}

// failingMockBee always fails to spawn
type failingMockBee struct {
	mockBee
}

func (m *failingMockBee) Spawn(ctx context.Context, t *task.Task) error {
	return errors.New("spawn failed")
}

func TestStatusConstants(t *testing.T) {
	tests := []struct {
		name     string
		status   Status
		expected string
	}{
		{"Idle", StatusIdle, "idle"},
		{"Running", StatusRunning, "running"},
		{"Stuck", StatusStuck, "stuck"},
		{"Complete", StatusComplete, "complete"},
		{"Failed", StatusFailed, "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.status) != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, tt.status)
			}
		})
	}
}

func TestNewPool(t *testing.T) {
	factory := func(id, adapter string) (Bee, error) {
		return newMockBee(id, adapter), nil
	}
	b := bus.New(100)
	pool := NewPool(5, factory, b)

	if pool == nil {
		t.Fatal("NewPool returned nil")
	}
	if pool.maxParallel != 5 {
		t.Errorf("Expected maxParallel 5, got %d", pool.maxParallel)
	}
	if pool.factory == nil {
		t.Error("factory not set")
	}
	if pool.msgBus != b {
		t.Error("bus not set correctly")
	}
	if pool.workers == nil {
		t.Error("workers map not initialized")
	}
}

func TestPoolSpawn(t *testing.T) {
	factory := func(id, adapter string) (Bee, error) {
		return newMockBee(id, adapter), nil
	}
	pool := NewPool(5, factory, nil)

	task1 := &task.Task{
		ID:     "task-1",
		Type:   task.TypeCode,
		Title:  "Test Task",
		Status: task.StatusPending,
	}

	ctx := context.Background()
	bee, err := pool.Spawn(ctx, task1, "test-adapter")

	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}
	if bee == nil {
		t.Fatal("Spawn returned nil bee")
	}
	if bee.ID() == "" {
		t.Error("Bee ID is empty")
	}

	// Verify worker is tracked
	retrieved, ok := pool.Get(bee.ID())
	if !ok {
		t.Error("Worker not found in pool")
	}
	if retrieved.ID() != bee.ID() {
		t.Error("Retrieved worker ID mismatch")
	}
}

func TestPoolSpawnMaxParallel(t *testing.T) {
	var spawnedCount atomic.Int32

	factory := func(id, adapter string) (Bee, error) {
		spawnedCount.Add(1)
		return newMockBee(id, adapter), nil
	}

	// Limit to 2 concurrent workers
	pool := NewPool(2, factory, nil)

	t1 := &task.Task{ID: "task-1", Type: task.TypeCode, Status: task.StatusPending}
	t2 := &task.Task{ID: "task-2", Type: task.TypeCode, Status: task.StatusPending}
	t3 := &task.Task{ID: "task-3", Type: task.TypeCode, Status: task.StatusPending}

	ctx := context.Background()

	// Spawn first worker
	bee1, err := pool.Spawn(ctx, t1, "adapter")
	if err != nil {
		t.Fatalf("First spawn failed: %v", err)
	}

	// Spawn second worker
	bee2, err := pool.Spawn(ctx, t2, "adapter")
	if err != nil {
		t.Fatalf("Second spawn failed: %v", err)
	}

	// Third spawn should fail (max parallel reached)
	_, err = pool.Spawn(ctx, t3, "adapter")
	if err == nil {
		t.Error("Expected error when max parallel reached")
	}
	if spawnedCount.Load() != 2 {
		t.Errorf("Expected 2 spawned workers, got %d", spawnedCount.Load())
	}

	// Clean up
	bee1.Kill()
	bee2.Kill()
}

func TestPoolSpawnFactoryError(t *testing.T) {
	factory := func(id, adapter string) (Bee, error) {
		return nil, errors.New("factory error")
	}
	pool := NewPool(5, factory, nil)

	task1 := &task.Task{ID: "task-1", Type: task.TypeCode, Status: task.StatusPending}
	ctx := context.Background()

	_, err := pool.Spawn(ctx, task1, "adapter")
	if err == nil {
		t.Error("Expected error when factory fails")
	}
}

func TestPoolSpawnSpawnError(t *testing.T) {
	factory := func(id, adapter string) (Bee, error) {
		return &failingMockBee{mockBee: *newMockBee(id, adapter)}, nil
	}
	pool := NewPool(5, factory, nil)

	task1 := &task.Task{ID: "task-1", Type: task.TypeCode, Status: task.StatusPending}
	ctx := context.Background()

	_, err := pool.Spawn(ctx, task1, "adapter")
	if err == nil {
		t.Error("Expected error when spawn fails")
	}
}

func TestPoolGet(t *testing.T) {
	factory := func(id, adapter string) (Bee, error) {
		return newMockBee(id, adapter), nil
	}
	pool := NewPool(5, factory, nil)

	// Get non-existent worker
	_, ok := pool.Get("non-existent")
	if ok {
		t.Error("Expected not to find non-existent worker")
	}

	task1 := &task.Task{ID: "task-1", Type: task.TypeCode, Status: task.StatusPending}
	ctx := context.Background()
	bee, _ := pool.Spawn(ctx, task1, "adapter")

	// Get existing worker
	retrieved, ok := pool.Get(bee.ID())
	if !ok {
		t.Error("Expected to find spawned worker")
	}
	if retrieved.ID() != bee.ID() {
		t.Error("Worker ID mismatch")
	}
}

func TestPoolActive(t *testing.T) {
	factory := func(id, adapter string) (Bee, error) {
		return newMockBee(id, adapter), nil
	}
	pool := NewPool(5, factory, nil)

	ctx := context.Background()

	// Initially no active workers
	active := pool.Active()
	if len(active) != 0 {
		t.Errorf("Expected 0 active workers, got %d", len(active))
	}

	// Spawn a worker
	task1 := &task.Task{ID: "task-1", Type: task.TypeCode, Status: task.StatusPending}
	bee, _ := pool.Spawn(ctx, task1, "adapter")

	// Worker should be active
	active = pool.Active()
	if len(active) != 1 {
		t.Errorf("Expected 1 active worker, got %d", len(active))
	}

	// Wait for completion
	time.Sleep(50 * time.Millisecond)

	// Worker should be complete, not active
	active = pool.Active()
	if len(active) != 0 {
		t.Errorf("Expected 0 active workers after completion, got %d", len(active))
	}

	bee.Kill()
}

func TestPoolActiveCount(t *testing.T) {
	factory := func(id, adapter string) (Bee, error) {
		return newMockBee(id, adapter), nil
	}
	pool := NewPool(5, factory, nil)

	if pool.ActiveCount() != 0 {
		t.Errorf("Expected 0 active count initially, got %d", pool.ActiveCount())
	}

	ctx := context.Background()
	task1 := &task.Task{ID: "task-1", Type: task.TypeCode, Status: task.StatusPending}
	pool.Spawn(ctx, task1, "adapter")

	if pool.ActiveCount() != 1 {
		t.Errorf("Expected 1 active count, got %d", pool.ActiveCount())
	}
}

func TestPoolKillAll(t *testing.T) {
	factory := func(id, adapter string) (Bee, error) {
		return newMockBee(id, adapter), nil
	}
	pool := NewPool(5, factory, nil)

	ctx := context.Background()

	// Spawn multiple workers
	bees := make([]Bee, 3)
	for i := 0; i < 3; i++ {
		task1 := &task.Task{ID: string(rune('a' + i)), Type: task.TypeCode, Status: task.StatusPending}
		bee, _ := pool.Spawn(ctx, task1, "adapter")
		bees[i] = bee
	}

	// Give workers time to start
	time.Sleep(20 * time.Millisecond)

	// Kill all
	pool.KillAll()

	// Verify all are no longer running
	for _, bee := range bees {
		status := bee.Monitor()
		if status == StatusRunning {
			t.Error("Worker should not be running after KillAll")
		}
	}
}

func TestPoolCleanup(t *testing.T) {
	factory := func(id, adapter string) (Bee, error) {
		return newMockBee(id, adapter), nil
	}
	pool := NewPool(5, factory, nil)

	ctx := context.Background()

	// Spawn workers
	for i := 0; i < 3; i++ {
		task1 := &task.Task{ID: string(rune('a' + i)), Type: task.TypeCode, Status: task.StatusPending}
		pool.Spawn(ctx, task1, "adapter")
	}

	// Wait for all to complete
	time.Sleep(50 * time.Millisecond)

	// Check workers exist
	if len(pool.workers) != 3 {
		t.Errorf("Expected 3 workers before cleanup, got %d", len(pool.workers))
	}

	// Cleanup
	pool.Cleanup()

	// Workers should be removed
	if len(pool.workers) != 0 {
		t.Errorf("Expected 0 workers after cleanup, got %d", len(pool.workers))
	}
}

func TestPoolSpawnWithBus(t *testing.T) {
	b := bus.New(100)
	var receivedMsg *bus.Message
	var mu sync.Mutex

	b.Subscribe(bus.MsgWorkerSpawned, func(msg bus.Message) {
		mu.Lock()
		defer mu.Unlock()
		receivedMsg = &msg
	})

	factory := func(id, adapter string) (Bee, error) {
		return newMockBee(id, adapter), nil
	}
	pool := NewPool(5, factory, b)

	task1 := &task.Task{ID: "task-1", Type: task.TypeCode, Title: "Test Task", Status: task.StatusPending}
	ctx := context.Background()
	bee, _ := pool.Spawn(ctx, task1, "adapter")

	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	if receivedMsg == nil {
		t.Error("Expected message to be published")
	} else {
		if receivedMsg.Type != bus.MsgWorkerSpawned {
			t.Errorf("Expected message type %s, got %s", bus.MsgWorkerSpawned, receivedMsg.Type)
		}
		if receivedMsg.WorkerID != bee.ID() {
			t.Errorf("Expected worker ID %s, got %s", bee.ID(), receivedMsg.WorkerID)
		}
		if receivedMsg.TaskID != "task-1" {
			t.Errorf("Expected task ID task-1, got %s", receivedMsg.TaskID)
		}
	}
	mu.Unlock()
}

func TestPoolConcurrency(t *testing.T) {
	factory := func(id, adapter string) (Bee, error) {
		return newMockBee(id, adapter), nil
	}
	pool := NewPool(10, factory, nil)

	var wg sync.WaitGroup
	ctx := context.Background()

	// Concurrent spawns
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			task1 := &task.Task{ID: string(rune('a' + id%26)), Type: task.TypeCode, Status: task.StatusPending}
			pool.Spawn(ctx, task1, "adapter")
		}(i)
	}

	wg.Wait()

	// Concurrent gets
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pool.Active()
			pool.ActiveCount()
		}()
	}

	wg.Wait()

	// Concurrent cleanup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pool.Cleanup()
		}()
	}

	wg.Wait()
}

func TestBeeInterface(t *testing.T) {
	mock := newMockBee("worker-1", "test-adapter")

	if mock.ID() != "worker-1" {
		t.Errorf("Expected ID worker-1, got %s", mock.ID())
	}
	if mock.Type() != "test-adapter" {
		t.Errorf("Expected type test-adapter, got %s", mock.Type())
	}
	if mock.Monitor() != StatusIdle {
		t.Errorf("Expected initial status idle, got %s", mock.Monitor())
	}

	// Test Spawn
	ctx := context.Background()
	task := &task.Task{ID: "task-1", Type: task.TypeCode, Status: task.StatusPending}
	err := mock.Spawn(ctx, task)
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	time.Sleep(20 * time.Millisecond)
	if mock.Monitor() != StatusComplete {
		t.Errorf("Expected status complete, got %s", mock.Monitor())
	}

	// Test Result
	result := mock.Result()
	if result == nil {
		t.Error("Expected non-nil result")
	} else if !result.Success {
		t.Error("Expected success to be true")
	}
}

// TestWorkerPanicRecovery verifies that workers can recover from panics
func TestWorkerPanicRecovery(t *testing.T) {
	// Create a mock bee that panics
	panickingBee := &panickingMockBee{
		mockBee: *newMockBee("panicky", "test"),
	}

	factory := func(id, adapter string) (Bee, error) {
		return panickingBee, nil
	}
	pool := NewPool(5, factory, nil)

	tsk := &task.Task{ID: "task-panic", Type: task.TypeCode, Status: task.StatusPending}
	ctx := context.Background()

	bee, err := pool.Spawn(ctx, tsk, "test-adapter")
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	// Wait for panic recovery
	time.Sleep(100 * time.Millisecond)

	// Worker should be failed after panic
	if bee.Monitor() != StatusFailed {
		t.Errorf("Expected status failed after panic, got %s", bee.Monitor())
	}

	// Result should contain panic information
	result := bee.Result()
	if result == nil {
		t.Fatal("Expected result after panic recovery")
	}
	if result.Success {
		t.Error("Expected success to be false after panic")
	}
	if len(result.Errors) == 0 {
		t.Error("Expected error messages after panic")
	}

	// Check that panic message is in errors
	foundPanic := false
	for _, err := range result.Errors {
		if strings.Contains(err, "panic:") {
			foundPanic = true
			break
		}
	}
	if !foundPanic {
		t.Errorf("Expected 'panic:' in errors, got: %v", result.Errors)
	}
}

func TestResultSchemaConformance(t *testing.T) {
	tests := []struct {
		name          string
		configureMock func() Bee
		wantSuccess   bool
		wantOutput    string
		wantErrors    int
	}{
		{
			name: "successful result with output",
			configureMock: func() Bee {
				m := newMockBee("test-1", "test-adapter")
				m.result = &task.Result{Success: true, Output: "task completed successfully"}
				m.status.Store(StatusComplete)
				return m
			},
			wantSuccess: true,
			wantOutput:  "task completed successfully",
			wantErrors:  0,
		},
		{
			name: "failed result with errors",
			configureMock: func() Bee {
				m := newMockBee("test-2", "test-adapter")
				m.result = &task.Result{Success: false, Output: "error occurred", Errors: []string{"error 1", "error 2"}}
				m.status.Store(StatusFailed)
				return m
			},
			wantSuccess: false,
			wantOutput:  "error occurred",
			wantErrors:  2,
		},
		{
			name: "result with artifacts",
			configureMock: func() Bee {
				m := newMockBee("test-3", "test-adapter")
				m.result = &task.Result{
					Success:   true,
					Output:    "files created",
					Artifacts: map[string]string{"file1.txt": "/path/to/file1.txt"},
				}
				m.status.Store(StatusComplete)
				return m
			},
			wantSuccess: true,
			wantOutput:  "files created",
			wantErrors:  0,
		},
		{
			name: "result with metrics",
			configureMock: func() Bee {
				m := newMockBee("test-4", "test-adapter")
				m.result = &task.Result{
					Success: true,
					Output:  "benchmark complete",
					Metrics: map[string]float64{"duration_ms": 150.5, "tokens": 500},
				}
				m.status.Store(StatusComplete)
				return m
			},
			wantSuccess: true,
			wantOutput:  "benchmark complete",
			wantErrors:  0,
		},
		{
			name: "result with empty output",
			configureMock: func() Bee {
				m := newMockBee("test-5", "test-adapter")
				m.result = &task.Result{Success: true, Output: ""}
				m.status.Store(StatusComplete)
				return m
			},
			wantSuccess: true,
			wantOutput:  "",
			wantErrors:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bee := tt.configureMock()

			result := bee.Result()
			if result == nil {
				t.Fatal("Result() returned nil")
			}

			if result.Success != tt.wantSuccess {
				t.Errorf("Result.Success = %v, want %v", result.Success, tt.wantSuccess)
			}

			if result.Output != tt.wantOutput {
				t.Errorf("Result.Output = %q, want %q", result.Output, tt.wantOutput)
			}

			if len(result.Errors) != tt.wantErrors {
				t.Errorf("Result.Errors length = %d, want %d", len(result.Errors), tt.wantErrors)
			}

			if result.Artifacts != nil {
				if _, ok := interface{}(result.Artifacts).(map[string]string); !ok {
					t.Error("Result.Artifacts should be map[string]string")
				}
			}

			if result.Metrics != nil {
				if _, ok := interface{}(result.Metrics).(map[string]float64); !ok {
					t.Error("Result.Metrics should be map[string]float64")
				}
			}
		})
	}
}

func TestResultSchemaFieldTypes(t *testing.T) {
	m := newMockBee("test", "test-adapter")
	m.result = &task.Result{
		Success:   true,
		Output:    "test output",
		Errors:    []string{"error1"},
		Artifacts: map[string]string{"key": "value"},
		Metrics:   map[string]float64{"count": 10.5},
	}
	m.status.Store(StatusComplete)

	result := m.Result()

	if _, ok := interface{}(result.Success).(bool); !ok {
		t.Error("Result.Success should be bool")
	}

	if _, ok := interface{}(result.Output).(string); !ok {
		t.Error("Result.Output should be string")
	}

	if _, ok := interface{}(result.Errors).([]string); !ok {
		t.Error("Result.Errors should be []string")
	}

	if _, ok := interface{}(result.Artifacts).(map[string]string); !ok {
		t.Error("Result.Artifacts should be map[string]string")
	}

	if _, ok := interface{}(result.Metrics).(map[string]float64); !ok {
		t.Error("Result.Metrics should be map[string]float64")
	}
}

func TestWorkerOutputMatchesResultOutput(t *testing.T) {
	m := newMockBee("test-1", "test-adapter")
	m.result = &task.Result{Success: true, Output: "hello world"}
	m.output = "hello world"
	m.status.Store(StatusComplete)

	result := m.Result()
	output := m.Output()

	if result.Output != output {
		t.Errorf("Result.Output = %q, worker.Output() = %q - should match", result.Output, output)
	}
}

// panickingMockBee simulates a worker that panics
type panickingMockBee struct {
	mockBee
	panicValue interface{}
}

func (m *panickingMockBee) Spawn(ctx context.Context, t *task.Task) error {
	m.status.Store(StatusRunning)

	// Simulate async work that panics
	go func() {
		time.Sleep(10 * time.Millisecond)
		// Simulate panic recovery like real adapters do
		defer func() {
			if r := recover(); r != nil {
				m.mu.Lock()
				defer m.mu.Unlock()
				m.status.Store(StatusFailed)
				panicMsg := fmt.Sprintf("panic: %v", r)
				m.result = &task.Result{
					Success: false,
					Errors:  []string{panicMsg},
				}
			}
		}()
		panic("simulated worker panic")
	}()

	return nil
}

func TestPoolSpawnWithTimeout(t *testing.T) {
	b := bus.New(100)
	factory := func(id, adapter string) (Bee, error) {
		m := newMockBee(id, adapter)
		// Simulate a long-running worker that takes 5s
		m.spawnFunc = func(ctx context.Context, tk *task.Task) error {
			go func() {
				m.status.Store(StatusRunning)
				select {
				case <-ctx.Done():
					m.mu.Lock()
					m.status.Store(StatusFailed)
					m.result = &task.Result{
						Success: false,
						Errors:  []string{"context cancelled"},
					}
					m.mu.Unlock()
				case <-time.After(5 * time.Second):
					m.mu.Lock()
					m.status.Store(StatusComplete)
					m.result = &task.Result{Success: true, Output: "done"}
					m.mu.Unlock()
				}
			}()
			return nil
		}
		return m, nil
	}

	pool := NewPool(4, factory, b)

	// Task with a 200ms timeout
	tk := &task.Task{
		ID:      "timeout-task",
		Type:    "test",
		Status:  task.StatusPending,
		Timeout: 200 * time.Millisecond,
	}

	bee, err := pool.Spawn(context.Background(), tk, "test")
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	// Wait for timeout to fire
	time.Sleep(500 * time.Millisecond)

	if bee.Monitor() != StatusFailed {
		t.Errorf("expected StatusFailed after timeout, got %s", bee.Monitor())
	}
}

func TestPoolSpawnNoTimeoutCompletes(t *testing.T) {
	factory := func(id, adapter string) (Bee, error) {
		m := newMockBee(id, adapter)
		m.spawnFunc = func(ctx context.Context, tk *task.Task) error {
			go func() {
				m.status.Store(StatusRunning)
				time.Sleep(50 * time.Millisecond)
				m.mu.Lock()
				m.status.Store(StatusComplete)
				m.result = &task.Result{Success: true, Output: "done"}
				m.mu.Unlock()
			}()
			return nil
		}
		return m, nil
	}

	pool := NewPool(4, factory, nil)

	// Task with no timeout (zero value)
	tk := &task.Task{
		ID:     "no-timeout-task",
		Type:   "test",
		Status: task.StatusPending,
	}

	bee, err := pool.Spawn(context.Background(), tk, "test")
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	if bee.Monitor() != StatusComplete {
		t.Errorf("expected StatusComplete, got %s", bee.Monitor())
	}
}
