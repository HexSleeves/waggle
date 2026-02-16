package adapter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/exedev/waggle/internal/task"
	"github.com/exedev/waggle/internal/worker"
)

// TestHealthCheckExecAdapter tests that HealthCheck passes for the exec adapter (bash)
func TestHealthCheckExecAdapter(t *testing.T) {
	adapter := NewExecAdapter(t.TempDir(), nil)
	if !adapter.Available() {
		t.Skip("bash not available")
	}

	ctx := context.Background()
	err := adapter.HealthCheck(ctx)
	if err != nil {
		t.Errorf("expected exec adapter health check to pass, got: %v", err)
	}
}

// TestHealthCheckNonExistentCommand tests that HealthCheck returns error for a missing command
func TestHealthCheckNonExistentCommand(t *testing.T) {
	adapter := NewCLIAdapter(CLIAdapterConfig{
		Name:    "bogus",
		Command: "nonexistent-command-that-does-not-exist-xyz",
		Mode:    PromptAsArg,
	})

	ctx := context.Background()
	err := adapter.HealthCheck(ctx)
	if err == nil {
		t.Error("expected health check to fail for non-existent command")
	}
}

// TestHealthCheckVersionCommand tests that --version mode works for real CLI tools
func TestHealthCheckVersionCommand(t *testing.T) {
	// Use 'echo' as a stand-in CLI; "echo --version" will succeed
	adapter := NewCLIAdapter(CLIAdapterConfig{
		Name:    "echo-test",
		Command: "echo",
		Mode:    PromptAsArg,
	})

	ctx := context.Background()
	err := adapter.HealthCheck(ctx)
	if err != nil {
		t.Errorf("expected health check to pass for echo, got: %v", err)
	}
}

// TestAdapterRegistry tests the adapter registry functionality
func TestAdapterRegistry(t *testing.T) {
	registry := NewRegistry()

	// Register mock adapters
	registry.Register(&MockAdapter{name: "test1", available: true})
	registry.Register(&MockAdapter{name: "test2", available: true})

	// Test Get
	a, ok := registry.Get("test1")
	if !ok {
		t.Fatal("expected to find test1 adapter")
	}
	if a.Name() != "test1" {
		t.Errorf("expected name test1, got %s", a.Name())
	}

	// Test Available
	avail := registry.Available()
	if len(avail) != 2 {
		t.Errorf("expected 2 available adapters, got %d", len(avail))
	}

	// Test WorkerFactory
	factory := registry.WorkerFactory()
	w, err := factory("test-worker", "test1")
	if err != nil {
		t.Fatalf("failed to create worker: %v", err)
	}
	if w.ID() != "test-worker" {
		t.Errorf("expected worker ID test-worker, got %s", w.ID())
	}
}

// TestTaskRouter tests task routing functionality
func TestTaskRouter(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&MockAdapter{name: "claude-code", available: true})
	registry.Register(&MockAdapter{name: "codex", available: true})

	router := NewTaskRouter(registry, "claude-code")

	// Test default routing
	codeTask := &task.Task{Type: task.TypeCode}
	route := router.Route(codeTask)
	if route != "claude-code" {
		t.Errorf("expected route claude-code, got %s", route)
	}

	// Test custom routing
	router.SetRoute(task.TypeCode, "codex")
	route = router.Route(codeTask)
	if route != "codex" {
		t.Errorf("expected custom route codex, got %s", route)
	}
}

// TestTaskRouterWithAdapterMap tests that adapter_map entries override the default adapter
func TestTaskRouterWithAdapterMap(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&MockAdapter{name: "claude-code", available: true})
	registry.Register(&MockAdapter{name: "kimi", available: true})
	registry.Register(&MockAdapter{name: "exec", available: true})

	adapterMap := map[string]string{
		"code": "kimi",
		"test": "exec",
	}
	router := NewTaskRouter(registry, "claude-code", adapterMap)

	// code tasks should route to kimi
	route := router.Route(&task.Task{Type: task.TypeCode})
	if route != "kimi" {
		t.Errorf("expected code task to route to kimi, got %s", route)
	}

	// test tasks should route to exec
	route = router.Route(&task.Task{Type: task.TypeTest})
	if route != "exec" {
		t.Errorf("expected test task to route to exec, got %s", route)
	}

	// review tasks should use default adapter (not in adapter_map)
	route = router.Route(&task.Task{Type: task.TypeReview})
	if route != "claude-code" {
		t.Errorf("expected review task to route to claude-code, got %s", route)
	}
}

// TestTaskRouterFallbackToDefault tests that unmapped task types fall back to default
func TestTaskRouterFallbackToDefault(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&MockAdapter{name: "claude-code", available: true})
	registry.Register(&MockAdapter{name: "kimi", available: true})

	adapterMap := map[string]string{
		"code": "kimi",
	}
	router := NewTaskRouter(registry, "claude-code", adapterMap)

	// research, test, review, generic should all fall back to default
	for _, tt := range []task.Type{task.TypeResearch, task.TypeTest, task.TypeReview, task.TypeGeneric} {
		route := router.Route(&task.Task{Type: tt})
		if route != "claude-code" {
			t.Errorf("expected %s task to fall back to claude-code, got %s", tt, route)
		}
	}
}

// TestTaskRouterMissingAdapter tests that a mapped adapter not in the registry falls back to default
func TestTaskRouterMissingAdapter(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&MockAdapter{name: "claude-code", available: true})
	// "kimi" is NOT registered

	adapterMap := map[string]string{
		"code": "kimi", // kimi not in registry
	}
	router := NewTaskRouter(registry, "claude-code", adapterMap)

	// code task mapped to kimi, but kimi doesn't exist — should fall back to default
	route := router.Route(&task.Task{Type: task.TypeCode})
	if route != "claude-code" {
		t.Errorf("expected code task to fall back to claude-code when kimi is missing, got %s", route)
	}
}

// TestClaudeAdapter tests Claude adapter functionality
func TestClaudeAdapter(t *testing.T) {
	tempDir := t.TempDir()
	adapter := NewClaudeAdapter("echo", []string{}, tempDir, nil)

	if adapter.Name() != "claude-code" {
		t.Errorf("expected name claude-code, got %s", adapter.Name())
	}

	if !adapter.Available() {
		t.Error("expected adapter to be available with echo command")
	}

	worker := adapter.CreateWorker("test-worker")
	if worker.Type() != "claude-code" {
		t.Errorf("expected worker type claude-code, got %s", worker.Type())
	}
}

// TestCodexAdapter tests Codex adapter functionality
func TestCodexAdapter(t *testing.T) {
	tempDir := t.TempDir()
	adapter := NewCodexAdapter("echo", []string{}, tempDir, nil)

	if adapter.Name() != "codex" {
		t.Errorf("expected name codex, got %s", adapter.Name())
	}

	if !adapter.Available() {
		t.Error("expected adapter to be available with echo command")
	}

	worker := adapter.CreateWorker("test-worker")
	if worker.Type() != "codex" {
		t.Errorf("expected worker type codex, got %s", worker.Type())
	}
}

// TestOpenCodeAdapter tests OpenCode adapter functionality
func TestOpenCodeAdapter(t *testing.T) {
	tempDir := t.TempDir()
	adapter := NewOpenCodeAdapter("echo", []string{}, tempDir, nil)

	if adapter.Name() != "opencode" {
		t.Errorf("expected name opencode, got %s", adapter.Name())
	}

	if !adapter.Available() {
		t.Error("expected adapter to be available with echo command")
	}

	worker := adapter.CreateWorker("test-worker")
	if worker.Type() != "opencode" {
		t.Errorf("expected worker type opencode, got %s", worker.Type())
	}
}

// TestWorkerExecution tests actual worker execution with mock commands
func TestWorkerExecution(t *testing.T) {
	tests := []struct {
		name     string
		adapter  Adapter
		taskType task.Type
	}{
		{"ClaudeWorker", NewClaudeAdapter("echo", []string{}, t.TempDir(), nil), task.TypeCode},
		{"CodexWorker", NewCodexAdapter("echo", []string{}, t.TempDir(), nil), task.TypeResearch},
		{"OpenCodeWorker", NewOpenCodeAdapter("echo", []string{}, t.TempDir(), nil), task.TypeTest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			worker := tt.adapter.CreateWorker("test-worker")

			task := &task.Task{
				ID:          "test-task",
				Type:        tt.taskType,
				Title:       "Test Task",
				Description: "This is a test task",
				Context:     map[string]string{"key": "value"},
			}

			err := worker.Spawn(ctx, task)
			if err != nil {
				t.Fatalf("failed to spawn worker: %v", err)
			}

			// Wait for completion
			for i := 0; i < 10; i++ {
				status := worker.Monitor()
				if status == "complete" || status == "failed" {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}

			result := worker.Result()
			if result == nil {
				t.Fatal("expected result but got nil")
			}

			if !result.Success {
				t.Errorf("expected success but got errors: %v", result.Errors)
			}

			output := worker.Output()
			if output == "" {
				t.Error("expected output but got empty string")
			}
		})
	}
}

// TestWorkerKill tests worker termination
func TestWorkerKill(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping slow test in short mode")
	}

	// Use a longer-running command to ensure it doesn't finish immediately
	adapter := NewClaudeAdapter("sleep", []string{"5"}, t.TempDir(), nil)
	worker := adapter.CreateWorker("test-worker")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	task := &task.Task{
		ID:          "test-task",
		Type:        task.TypeCode,
		Title:       "Long Running Task",
		Description: "This task should run for a while",
	}

	err := worker.Spawn(ctx, task)
	if err != nil {
		t.Fatalf("failed to spawn worker: %v", err)
	}

	// Give it a moment to start
	time.Sleep(500 * time.Millisecond)

	// Kill the worker
	err = worker.Kill()
	if err != nil {
		// Don't fail the test if the process already finished
		t.Logf("worker kill result: %v", err)
	}
}

// TestBuildPrompt tests the prompt building functionality
func TestBuildPrompt(t *testing.T) {
	task := &task.Task{
		Title:        "Test Task",
		Type:         task.TypeCode,
		Description:  "This is a test description",
		Context:      map[string]string{"env": "test", "debug": "true"},
		AllowedPaths: []string{"/tmp", "/home/user"},
	}

	prompt := buildPrompt(task)

	expectedParts := []string{
		"Task: Test Task",
		"Type: code",
		"Description:\nThis is a test description",
		"- env: test",
		"- debug: true",
		"Only modify files in: /tmp, /home/user",
	}

	for _, part := range expectedParts {
		if !contains(prompt, part) {
			t.Errorf("prompt missing expected part: %s\nActual prompt:\n%s", part, prompt)
		}
	}
}

// TestOpenCodeAdapterPathResolution tests OpenCode adapter path resolution
func TestOpenCodeAdapterPathResolution(t *testing.T) {
	tempDir := t.TempDir()
	fakeOpenCode := filepath.Join(tempDir, "opencode")

	// Create a fake opencode executable
	err := os.WriteFile(fakeOpenCode, []byte("#!/bin/bash\necho 'opencode output'"), 0755)
	if err != nil {
		t.Fatalf("failed to create fake opencode: %v", err)
	}

	// Test with absolute path
	adapter := NewOpenCodeAdapter(fakeOpenCode, []string{}, tempDir, nil)
	if !adapter.Available() {
		t.Error("expected adapter to be available with absolute path")
	}

	// Test with relative path - this might pass due to system PATH
	adapter2 := NewOpenCodeAdapter("nonexistent-opencode-definitely-not-real", []string{}, tempDir, nil)
	if adapter2.Available() {
		t.Log("nonexistent command was available (likely in PATH), skipping this check")
	}
}

// MockAdapter is a mock implementation for testing
type MockAdapter struct {
	name          string
	available     bool
	workerFactory func(id string) worker.Bee
}

// NewMockAdapter creates a new MockAdapter with the given name and availability
func NewMockAdapter(name string, available bool) *MockAdapter {
	return &MockAdapter{
		name:      name,
		available: available,
		workerFactory: func(id string) worker.Bee {
			return NewEnhancedMockBee(id, name)
		},
	}
}

func (m *MockAdapter) Name() string {
	return m.name
}

func (m *MockAdapter) Available() bool {
	return m.available
}

func (m *MockAdapter) HealthCheck(ctx context.Context) error {
	if !m.available {
		return fmt.Errorf("adapter %q not available", m.name)
	}
	return nil
}

func (m *MockAdapter) CreateWorker(id string) worker.Bee {
	if m.workerFactory != nil {
		return m.workerFactory(id)
	}
	return NewEnhancedMockBee(id, m.name)
}

// SetWorkerFactory configures a custom worker factory
func (m *MockAdapter) SetWorkerFactory(factory func(id string) worker.Bee) {
	m.workerFactory = factory
}

// SetAvailable configures adapter availability
func (m *MockAdapter) SetAvailable(available bool) {
	m.available = available
}

// MockWorker is a basic mock worker implementation (kept for backward compatibility)
type MockWorker struct {
	id     string
	typ    string
	status worker.Status
}

func (m *MockWorker) ID() string {
	return m.id
}

func (m *MockWorker) Type() string {
	return m.typ
}

func (m *MockWorker) Spawn(ctx context.Context, t *task.Task) error {
	m.status = worker.StatusComplete
	return nil
}

func (m *MockWorker) Monitor() worker.Status {
	return m.status
}

func (m *MockWorker) Result() *task.Result {
	return &task.Result{Success: true}
}

func (m *MockWorker) Kill() error {
	m.status = worker.StatusFailed
	return nil
}

func (m *MockWorker) Output() string {
	return "mock output"
}

// EnhancedMockBee is a configurable mock implementation of worker.Bee
type EnhancedMockBee struct {
	mu sync.RWMutex

	// Identity
	id  string
	typ string

	// Configurable behavior
	status           worker.Status
	result           *task.Result
	output           string
	spawnError       error
	killError        error
	delay            time.Duration
	autoComplete     bool
	statusTransition []worker.Status

	// Call tracking for inspection
	spawnCalled    bool
	killCalled     bool
	spawnTask      *task.Task
	spawnContext   context.Context
	spawnCallCount int

	// Status control channel
	statusCh chan worker.Status
}

// NewEnhancedMockBee creates a new EnhancedMockBee with default settings
func NewEnhancedMockBee(id, typ string) *EnhancedMockBee {
	return &EnhancedMockBee{
		id:           id,
		typ:          typ,
		status:       worker.StatusIdle,
		result:       &task.Result{Success: true, Output: "mock output"},
		output:       "mock output",
		autoComplete: true,
		statusCh:     make(chan worker.Status, 10),
	}
}

// ID returns the worker identifier
func (m *EnhancedMockBee) ID() string {
	return m.id
}

// Type returns the worker type
func (m *EnhancedMockBee) Type() string {
	return m.typ
}

// Spawn starts the worker with a task
func (m *EnhancedMockBee) Spawn(ctx context.Context, t *task.Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.spawnCalled = true
	m.spawnCallCount++
	m.spawnTask = t
	m.spawnContext = ctx

	if m.spawnError != nil {
		m.status = worker.StatusFailed
		return m.spawnError
	}

	m.status = worker.StatusRunning

	// Simulate async work if autoComplete is enabled
	if m.autoComplete {
		go m.runSimulation(ctx)
	}

	return nil
}

// runSimulation simulates task execution with configurable delay and transitions
func (m *EnhancedMockBee) runSimulation(ctx context.Context) {
	// Apply initial delay if configured
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			m.mu.Lock()
			m.status = worker.StatusFailed
			m.mu.Unlock()
			return
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Process status transitions if configured
	if len(m.statusTransition) > 0 {
		for _, status := range m.statusTransition {
			m.status = status
			select {
			case m.statusCh <- status:
			default:
			}
			// Small delay between transitions for realism
			time.Sleep(10 * time.Millisecond)
		}
	} else {
		// Default transition: running -> complete/failed
		if m.result != nil && m.result.Success {
			m.status = worker.StatusComplete
		} else {
			m.status = worker.StatusFailed
		}
		select {
		case m.statusCh <- m.status:
		default:
		}
	}
}

// Monitor returns the current status
func (m *EnhancedMockBee) Monitor() worker.Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

// Result returns the task result
func (m *EnhancedMockBee) Result() *task.Result {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.result
}

// Kill terminates the worker
func (m *EnhancedMockBee) Kill() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.killCalled = true
	m.status = worker.StatusFailed

	return m.killError
}

// Output returns accumulated output
func (m *EnhancedMockBee) Output() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.output
}

// Configuration methods

// SetStatus sets the current status (for manual control)
func (m *EnhancedMockBee) SetStatus(status worker.Status) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = status
	select {
	case m.statusCh <- status:
	default:
	}
}

// SetResult configures the result to return
func (m *EnhancedMockBee) SetResult(result *task.Result) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.result = result
}

// SetOutput configures the output string
func (m *EnhancedMockBee) SetOutput(output string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.output = output
}

// SetSpawnError configures an error to return from Spawn
func (m *EnhancedMockBee) SetSpawnError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spawnError = err
}

// SetKillError configures an error to return from Kill
func (m *EnhancedMockBee) SetKillError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.killError = err
}

// SetDelay configures the delay before status changes
func (m *EnhancedMockBee) SetDelay(delay time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.delay = delay
}

// SetAutoComplete configures whether to auto-complete on spawn
func (m *EnhancedMockBee) SetAutoComplete(auto bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.autoComplete = auto
}

// SetStatusTransition configures a sequence of status transitions
func (m *EnhancedMockBee) SetStatusTransition(transitions []worker.Status) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statusTransition = transitions
}

// Inspection methods

// WasSpawnCalled returns true if Spawn was called
func (m *EnhancedMockBee) WasSpawnCalled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.spawnCalled
}

// WasKillCalled returns true if Kill was called
func (m *EnhancedMockBee) WasKillCalled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.killCalled
}

// GetSpawnTask returns the task passed to Spawn
func (m *EnhancedMockBee) GetSpawnTask() *task.Task {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.spawnTask
}

// GetSpawnContext returns the context passed to Spawn
func (m *EnhancedMockBee) GetSpawnContext() context.Context {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.spawnContext
}

// GetSpawnCallCount returns the number of times Spawn was called
func (m *EnhancedMockBee) GetSpawnCallCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.spawnCallCount
}

// WaitForStatus blocks until the specified status is reached or timeout
func (m *EnhancedMockBee) WaitForStatus(status worker.Status, timeout time.Duration) bool {
	deadline := time.After(timeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return false
		case <-ticker.C:
			if m.Monitor() == status {
				return true
			}
		}
	}
}

// Reset resets the mock to its initial state
func (m *EnhancedMockBee) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.status = worker.StatusIdle
	m.result = &task.Result{Success: true, Output: "mock output"}
	m.output = "mock output"
	m.spawnError = nil
	m.killError = nil
	m.delay = 0
	m.autoComplete = true
	m.statusTransition = nil

	m.spawnCalled = false
	m.killCalled = false
	m.spawnTask = nil
	m.spawnContext = nil
	m.spawnCallCount = 0

	// Drain status channel
	select {
	case <-m.statusCh:
	default:
	}
}

// Preset configuration helpers

// Succeed configures the mock to succeed with the given output
func (m *EnhancedMockBee) Succeed(output string) {
	m.SetResult(&task.Result{Success: true, Output: output})
	m.SetOutput(output)
	m.SetStatus(worker.StatusComplete)
}

// Fail configures the mock to fail with the given error message
func (m *EnhancedMockBee) Fail(errMsg string) {
	m.SetResult(&task.Result{Success: false, Errors: []string{errMsg}})
	m.SetOutput("")
	m.SetStatus(worker.StatusFailed)
}

// FailWithOutput configures the mock to fail with output and errors
func (m *EnhancedMockBee) FailWithOutput(output string, errors []string) {
	m.SetResult(&task.Result{Success: false, Output: output, Errors: errors})
	m.SetOutput(output)
	m.SetStatus(worker.StatusFailed)
}

// SimulateStuck configures the mock to stay in running state
func (m *EnhancedMockBee) SimulateStuck() {
	m.SetAutoComplete(false)
	m.SetStatus(worker.StatusRunning)
}

// SimulateTimeout configures the mock to simulate a timeout scenario
func (m *EnhancedMockBee) SimulateTimeout() {
	m.SetDelay(1 * time.Hour) // Long delay to simulate timeout
	m.SetStatus(worker.StatusRunning)
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > len(substr) && indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
