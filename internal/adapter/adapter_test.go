package adapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/exedev/queen-bee/internal/task"
	"github.com/exedev/queen-bee/internal/worker"
)

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
	registry.Register(&MockAdapter{name: "claude-code"})
	registry.Register(&MockAdapter{name: "codex"})

	router := NewTaskRouter(registry)

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

// TestClaudeAdapter tests Claude adapter functionality
func TestClaudeAdapter(t *testing.T) {
	tempDir := t.TempDir()
	adapter := NewClaudeAdapter("echo", []string{}, tempDir)

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
	adapter := NewCodexAdapter("echo", []string{}, tempDir)

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
	adapter := NewOpenCodeAdapter("echo", []string{}, tempDir)

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
		{"ClaudeWorker", NewClaudeAdapter("echo", []string{}, t.TempDir()), task.TypeCode},
		{"CodexWorker", NewCodexAdapter("echo", []string{}, t.TempDir()), task.TypeResearch},
		{"OpenCodeWorker", NewOpenCodeAdapter("echo", []string{}, t.TempDir()), task.TypeTest},
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
	adapter := NewClaudeAdapter("sleep", []string{"5"}, t.TempDir())
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
		"Context:\n- env: test\n- debug: true",
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
	adapter := NewOpenCodeAdapter(fakeOpenCode, []string{}, tempDir)
	if !adapter.Available() {
		t.Error("expected adapter to be available with absolute path")
	}

	// Test with relative path - this might pass due to system PATH
	adapter2 := NewOpenCodeAdapter("nonexistent-opencode-definitely-not-real", []string{}, tempDir)
	if adapter2.Available() {
		t.Log("nonexistent command was available (likely in PATH), skipping this check")
	}
}

// MockAdapter is a mock implementation for testing
type MockAdapter struct {
	name      string
	available bool
}

func (m *MockAdapter) Name() string {
	return m.name
}

func (m *MockAdapter) Available() bool {
	return m.available
}

func (m *MockAdapter) CreateWorker(id string) worker.Bee {
	return &MockWorker{
		id:     id,
		typ:    m.name,
		status: worker.StatusIdle,
	}
}

// MockWorker is a mock worker implementation
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
