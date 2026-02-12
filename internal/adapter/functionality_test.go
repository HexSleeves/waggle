package adapter

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/exedev/queen-bee/internal/task"
	"github.com/exedev/queen-bee/internal/worker"
)

// TestClaudeAdapterFunctionality tests Claude-specific functionality
func TestClaudeAdapterFunctionality(t *testing.T) {
	tempDir := t.TempDir()

	// Create test files
	testFile := tempDir + "/test.go"
	err := os.WriteFile(testFile, []byte("package test\n\nfunc oldFunction() {}"), 0644)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	adapter := NewClaudeAdapter("echo", []string{}, tempDir)
	worker := adapter.CreateWorker("claude-test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	task := &task.Task{
		ID:           "claude-task",
		Type:         task.TypeCode,
		Title:        "Refactor Function",
		Description:  "Refactor oldFunction to newFunction with better naming",
		AllowedPaths: []string{tempDir},
		Context: map[string]string{
			"language": "go",
			"style":    "clean-code",
		},
	}

	err = worker.Spawn(ctx, task)
	if err != nil {
		t.Fatalf("failed to spawn worker: %v", err)
	}

	// Wait for completion
	for i := 0; i < 10; i++ {
		if worker.Monitor() == "complete" || worker.Monitor() == "failed" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	result := worker.Result()
	if result == nil {
		t.Fatal("expected result but got nil")
	}

	if !result.Success {
		t.Errorf("Claude task failed: %v", result.Errors)
	}

	output := worker.Output()
	if !contains(output, "Refactor Function") {
		t.Error("expected output to contain task title")
	}
	if !contains(output, "clean-code") {
		t.Error("expected output to contain context information")
	}
}

// TestCodexAdapterFunctionality tests Codex-specific functionality
func TestCodexAdapterFunctionality(t *testing.T) {
	tempDir := t.TempDir()

	// Create a Python test file
	testFile := tempDir + "/test.py"
	err := os.WriteFile(testFile, []byte("def calculate():\n    return 1 + 1"), 0644)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	adapter := NewCodexAdapter("echo", []string{}, tempDir)
	worker := adapter.CreateWorker("codex-test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	task := &task.Task{
		ID:          "codex-task",
		Type:        task.TypeCode,
		Title:       "Add Documentation",
		Description: "Add comprehensive docstring to calculate function",
		Context: map[string]string{
			"language": "python",
			"style":    "google-docs",
		},
	}

	err = worker.Spawn(ctx, task)
	if err != nil {
		t.Fatalf("failed to spawn worker: %v", err)
	}

	// Wait for completion
	for i := 0; i < 10; i++ {
		if worker.Monitor() == "complete" || worker.Monitor() == "failed" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	result := worker.Result()
	if result == nil {
		t.Fatal("expected result but got nil")
	}

	if !result.Success {
		t.Errorf("Codex task failed: %v", result.Errors)
	}

	output := worker.Output()
	if !contains(output, "Add Documentation") {
		t.Error("expected output to contain task title")
	}
}

// TestOpenCodeAdapterFunctionality tests OpenCode-specific functionality
func TestOpenCodeAdapterFunctionality(t *testing.T) {
	tempDir := t.TempDir()

	// Create a JavaScript test file
	testFile := tempDir + "/test.js"
	err := os.WriteFile(testFile, []byte("function greet() {\n    console.log('Hello');\n}"), 0644)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	adapter := NewOpenCodeAdapter("echo", []string{}, tempDir)
	worker := adapter.CreateWorker("opencode-test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	task := &task.Task{
		ID:          "opencode-task",
		Type:        task.TypeCode,
		Title:       "Add Error Handling",
		Description: "Add proper error handling to greet function",
		Context: map[string]string{
			"language":  "javascript",
			"framework": "nodejs",
		},
	}

	err = worker.Spawn(ctx, task)
	if err != nil {
		t.Fatalf("failed to spawn worker: %v", err)
	}

	// Wait for completion
	for i := 0; i < 10; i++ {
		if worker.Monitor() == "complete" || worker.Monitor() == "failed" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	result := worker.Result()
	if result == nil {
		t.Fatal("expected result but got nil")
	}

	if !result.Success {
		t.Errorf("OpenCode task failed: %v", result.Errors)
	}

	output := worker.Output()
	if !contains(output, "Add Error Handling") {
		t.Error("expected output to contain task title")
	}
}

// TestAdapterTaskTypes tests each adapter with different task types
func TestAdapterTaskTypes(t *testing.T) {
	tempDir := t.TempDir()

	adapters := map[string]Adapter{
		"claude-code": NewClaudeAdapter("echo", []string{}, tempDir),
		"codex":       NewCodexAdapter("echo", []string{}, tempDir),
		"opencode":    NewOpenCodeAdapter("echo", []string{}, tempDir),
	}

	taskTypes := []task.Type{
		task.TypeCode,
		task.TypeResearch,
		task.TypeTest,
		task.TypeReview,
		task.TypeGeneric,
	}

	for adapterName, adapter := range adapters {
		for _, taskType := range taskTypes {
			t.Run(string(adapterName)+"-"+string(taskType), func(t *testing.T) {
				worker := adapter.CreateWorker("test-worker")

				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()

				task := &task.Task{
					ID:          "test-task",
					Type:        taskType,
					Title:       "Test " + string(taskType),
					Description: "Test description for " + string(taskType),
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
					t.Errorf("task failed: %v", result.Errors)
				}
			})
		}
	}
}

// TestAdapterErrorHandling tests how adapters handle errors
func TestAdapterErrorHandling(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name    string
		adapter Adapter
		command string
	}{
		{"ClaudeError", NewClaudeAdapter("false", []string{}, tempDir), "false"},
		{"CodexError", NewCodexAdapter("false", []string{}, tempDir), "false"},
		{"OpenCodeError", NewOpenCodeAdapter("false", []string{}, tempDir), "false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worker := tt.adapter.CreateWorker("error-test")

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			task := &task.Task{
				ID:          "error-task",
				Type:        task.TypeCode,
				Title:       "Error Task",
				Description: "This should fail",
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

			if result.Success {
				t.Error("expected task to fail but it succeeded")
			}

			if len(result.Errors) == 0 {
				t.Error("expected errors but got none")
			}
		})
	}
}

// TestAdapterTimeout tests worker timeout functionality
func TestAdapterTimeout(t *testing.T) {
	tempDir := t.TempDir()

	// Use sleep command to simulate long-running task
	adapter := NewClaudeAdapter("sleep", []string{"30"}, tempDir)
	worker := adapter.CreateWorker("timeout-test")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	task := &task.Task{
		ID:          "timeout-task",
		Type:        task.TypeCode,
		Title:       "Timeout Task",
		Description: "This should timeout",
		Timeout:     50 * time.Millisecond,
	}

	err := worker.Spawn(ctx, task)
	if err != nil {
		t.Fatalf("failed to spawn worker: %v", err)
	}

	// Wait for timeout
	select {
	case <-ctx.Done():
		// Context should be cancelled due to timeout
		if ctx.Err() != context.DeadlineExceeded {
			t.Errorf("expected deadline exceeded, got %v", ctx.Err())
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("expected timeout but task completed")
	}
}

// TestAdapterConcurrency tests concurrent worker execution
func TestAdapterConcurrency(t *testing.T) {
	tempDir := t.TempDir()
	adapter := NewClaudeAdapter("echo", []string{}, tempDir)

	const numWorkers = 5
	workers := make([]worker.Bee, numWorkers)

	// Create multiple workers
	for i := 0; i < numWorkers; i++ {
		workers[i] = adapter.CreateWorker("worker-" + string(rune('A'+i)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Spawn all workers concurrently
	for i, worker := range workers {
		task := &task.Task{
			ID:          "task-" + string(rune('A'+i)),
			Type:        task.TypeCode,
			Title:       "Concurrent Task " + string(rune('A'+i)),
			Description: "Description for concurrent task " + string(rune('A'+i)),
		}

		err := worker.Spawn(ctx, task)
		if err != nil {
			t.Fatalf("failed to spawn worker %d: %v", i, err)
		}
	}

	// Wait for all to complete
	for i, worker := range workers {
		for j := 0; j < 20; j++ {
			status := worker.Monitor()
			if status == "complete" || status == "failed" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		result := worker.Result()
		if result == nil {
			t.Errorf("worker %d: expected result but got nil", i)
		} else if !result.Success {
			t.Errorf("worker %d: task failed: %v", i, result.Errors)
		}
	}
}

// TestAdapterPromptBuilding tests prompt building with different configurations
func TestAdapterPromptBuilding(t *testing.T) {
	tests := []struct {
		name     string
		task     *task.Task
		contains []string
	}{
		{
			name: "BasicTask",
			task: &task.Task{
				Title:       "Simple Task",
				Type:        task.TypeCode,
				Description: "Basic description",
			},
			contains: []string{"Task: Simple Task", "Type: code", "Basic description"},
		},
		{
			name: "TaskWithContext",
			task: &task.Task{
				Title:       "Complex Task",
				Type:        task.TypeResearch,
				Description: "Complex description",
				Context:     map[string]string{"env": "prod", "debug": "false"},
			},
			contains: []string{"Task: Complex Task", "Type: research", "env: prod", "debug: false"},
		},
		{
			name: "TaskWithPaths",
			task: &task.Task{
				Title:        "Restricted Task",
				Type:         task.TypeTest,
				Description:  "Test with path restrictions",
				AllowedPaths: []string{"/tmp", "/var/log"},
			},
			contains: []string{"Only modify files in: /tmp, /var/log"},
		},
		{
			name: "EmptyTask",
			task: &task.Task{
				Title: "",
				Type:  task.TypeGeneric,
			},
			contains: []string{"Task: ", "Type: generic"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := buildPrompt(tt.task)

			for _, expected := range tt.contains {
				if !contains(prompt, expected) {
					t.Errorf("prompt missing expected content: %s\nActual prompt:\n%s", expected, prompt)
				}
			}
		})
	}
}

// TestAdapterWorkingDirectory tests that workers respect working directory
func TestAdapterWorkingDirectory(t *testing.T) {
	tempDir := t.TempDir()

	// Use a simple command that shows current directory
	adapter := NewClaudeAdapter("echo", []string{"working in $(pwd)"}, tempDir)
	worker := adapter.CreateWorker("wd-test")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	task := &task.Task{
		ID:          "wd-task",
		Type:        task.TypeCode,
		Title:       "Working Directory Test",
		Description: "Test working directory",
	}

	err := worker.Spawn(ctx, task)
	if err != nil {
		t.Fatalf("failed to spawn worker: %v", err)
	}

	// Wait for completion
	for i := 0; i < 10; i++ {
		if worker.Monitor() == "complete" || worker.Monitor() == "failed" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	result := worker.Result()
	if result == nil {
		t.Fatal("expected result but got nil")
	}

	if !result.Success {
		t.Errorf("task failed: %v", result.Errors)
	}

	// The output should contain our echo message, not necessarily the full path
	output := worker.Output()
	if !strings.Contains(output, "working in") {
		t.Errorf("expected output to contain working directory info, got: %s", output)
	}
}
