package adapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/exedev/queen-bee/internal/config"
	"github.com/exedev/queen-bee/internal/safety"
	"github.com/exedev/queen-bee/internal/task"
)

// TestSafetyGuardBlocksDangerousCommands tests that blocked commands are rejected
func TestSafetyGuardBlocksDangerousCommands(t *testing.T) {
	tempDir := t.TempDir()

	// Create a guard with blocked commands
	safetyCfg := config.SafetyConfig{
		AllowedPaths:    []string{tempDir},
		BlockedCommands: []string{"rm -rf /", "sudo", "mkfs", "dd if=/dev/zero"},
		MaxFileSize:     10 * 1024 * 1024,
	}

	guard, err := safety.NewGuard(safetyCfg, tempDir)
	if err != nil {
		t.Fatalf("failed to create guard: %v", err)
	}

	// Test exec adapter with blocked command
	execAdapter := NewExecAdapter(tempDir, guard)
	worker := execAdapter.CreateWorker("safety-test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try to execute a blocked command
	taskWithBlockedCmd := &task.Task{
		ID:          "blocked-task",
		Type:        task.TypeGeneric,
		Title:       "Blocked Command Test",
		Description: "This task tries to run sudo rm -rf /",
		Context: map[string]string{
			"command": "sudo rm -rf /",
		},
	}

	err = worker.Spawn(ctx, taskWithBlockedCmd)
	// The spawn should succeed (async), but the worker should fail immediately
	if err != nil {
		t.Fatalf("spawn should not return error for safety check (async): %v", err)
	}

	// Wait for the worker to complete
	for i := 0; i < 20; i++ {
		status := worker.Monitor()
		if status == "failed" || status == "complete" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	result := worker.Result()
	if result == nil {
		t.Fatal("expected result but got nil")
	}

	if result.Success {
		t.Error("expected task to fail due to blocked command")
	}

	if len(result.Errors) == 0 {
		t.Error("expected error messages but got none")
	}

	// Check that the error mentions the blocked command
	hasBlockedError := false
	for _, errMsg := range result.Errors {
		if containsStr(errMsg, "blocked") || containsStr(errMsg, "safety") {
			hasBlockedError = true
			break
		}
	}
	if !hasBlockedError {
		t.Errorf("expected error to mention 'blocked' or 'safety', got: %v", result.Errors)
	}
}

// TestSafetyGuardPathRestrictions tests that path restrictions are enforced
func TestSafetyGuardPathRestrictions(t *testing.T) {
	tempDir := t.TempDir()
	outsideDir := "/tmp/outside"

	// Create a guard with restricted paths
	safetyCfg := config.SafetyConfig{
		AllowedPaths:    []string{tempDir},
		BlockedCommands: []string{},
		MaxFileSize:     10 * 1024 * 1024,
	}

	guard, err := safety.NewGuard(safetyCfg, tempDir)
	if err != nil {
		t.Fatalf("failed to create guard: %v", err)
	}

	// Test with a path outside allowed paths
	claudeAdapter := NewClaudeAdapter("echo", []string{}, tempDir, guard)
	worker := claudeAdapter.CreateWorker("path-test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	taskWithBadPath := &task.Task{
		ID:           "path-task",
		Type:         task.TypeCode,
		Title:        "Path Restriction Test",
		Description:  "This task has paths outside allowed directories",
		AllowedPaths: []string{outsideDir},
	}

	err = worker.Spawn(ctx, taskWithBadPath)
	if err != nil {
		t.Fatalf("spawn should not return error for path check (async): %v", err)
	}

	// Wait for completion
	for i := 0; i < 20; i++ {
		status := worker.Monitor()
		if status == "failed" || status == "complete" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	result := worker.Result()
	if result == nil {
		t.Fatal("expected result but got nil")
	}

	if result.Success {
		t.Error("expected task to fail due to path restriction")
	}

	if len(result.Errors) == 0 {
		t.Error("expected error messages but got none")
	}

	// Check that the error mentions path validation
	hasPathError := false
	for _, errMsg := range result.Errors {
		if containsStr(errMsg, "path") || containsStr(errMsg, "outside") {
			hasPathError = true
			break
		}
	}
	if !hasPathError {
		t.Errorf("expected error to mention path validation, got: %v", result.Errors)
	}
}

// TestSafetyGuardValidPathsAllowed tests that valid paths are allowed
func TestSafetyGuardValidPathsAllowed(t *testing.T) {
	tempDir := t.TempDir()
	allowedSubdir := filepath.Join(tempDir, "allowed")

	if err := os.MkdirAll(allowedSubdir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}

	// Create a guard with restricted paths
	safetyCfg := config.SafetyConfig{
		AllowedPaths:    []string{allowedSubdir},
		BlockedCommands: []string{},
		MaxFileSize:     10 * 1024 * 1024,
	}

	guard, err := safety.NewGuard(safetyCfg, tempDir)
	if err != nil {
		t.Fatalf("failed to create guard: %v", err)
	}

	// Test with a valid path inside allowed paths
	claudeAdapter := NewClaudeAdapter("echo", []string{}, tempDir, guard)
	worker := claudeAdapter.CreateWorker("valid-path-test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	taskWithValidPath := &task.Task{
		ID:           "valid-path-task",
		Type:         task.TypeCode,
		Title:        "Valid Path Test",
		Description:  "This task has valid allowed paths",
		AllowedPaths: []string{allowedSubdir},
	}

	err = worker.Spawn(ctx, taskWithValidPath)
	if err != nil {
		t.Fatalf("failed to spawn worker: %v", err)
	}

	// Wait for completion
	for i := 0; i < 20; i++ {
		status := worker.Monitor()
		if status == "failed" || status == "complete" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	result := worker.Result()
	if result == nil {
		t.Fatal("expected result but got nil")
	}

	// This should succeed since the path is allowed
	if !result.Success {
		t.Errorf("expected task to succeed with valid paths, got errors: %v", result.Errors)
	}
}

// TestSafetyGuardReadOnlyMode tests read-only mode behavior
func TestSafetyGuardReadOnlyMode(t *testing.T) {
	tempDir := t.TempDir()

	// Create a guard in read-only mode
	safetyCfg := config.SafetyConfig{
		AllowedPaths:    []string{tempDir},
		BlockedCommands: []string{},
		ReadOnlyMode:    true,
		MaxFileSize:     10 * 1024 * 1024,
	}

	guard, err := safety.NewGuard(safetyCfg, tempDir)
	if err != nil {
		t.Fatalf("failed to create guard: %v", err)
	}

	if !guard.IsReadOnly() {
		t.Error("expected guard to be in read-only mode")
	}

	// Test that the warning appears in output
	claudeAdapter := NewClaudeAdapter("echo", []string{}, tempDir, guard)
	worker := claudeAdapter.CreateWorker("readonly-test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	testTask := &task.Task{
		ID:          "readonly-task",
		Type:        task.TypeCode,
		Title:       "Read-Only Mode Test",
		Description: "Testing read-only mode warning",
	}

	err = worker.Spawn(ctx, testTask)
	if err != nil {
		t.Fatalf("failed to spawn worker: %v", err)
	}

	// Wait for completion
	for i := 0; i < 20; i++ {
		status := worker.Monitor()
		if status == "failed" || status == "complete" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	output := worker.Output()
	if !containsStr(output, "SAFETY WARNING") || !containsStr(output, "read-only") {
		t.Errorf("expected output to contain read-only mode warning, got: %s", output)
	}
}

// TestSafetyGuardNilGuard tests that nil guard allows all operations
func TestSafetyGuardNilGuard(t *testing.T) {
	tempDir := t.TempDir()

	// Test with nil guard - should allow everything
	claudeAdapter := NewClaudeAdapter("echo", []string{}, tempDir, nil)
	worker := claudeAdapter.CreateWorker("nil-guard-test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	testTask := &task.Task{
		ID:           "nil-guard-task",
		Type:         task.TypeCode,
		Title:        "Nil Guard Test",
		Description:  "This task should work with nil guard",
		AllowedPaths: []string{"/any/path"},
	}

	err := worker.Spawn(ctx, testTask)
	if err != nil {
		t.Fatalf("failed to spawn worker with nil guard: %v", err)
	}

	// Wait for completion
	for i := 0; i < 20; i++ {
		status := worker.Monitor()
		if status == "failed" || status == "complete" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	result := worker.Result()
	if result == nil {
		t.Fatal("expected result but got nil")
	}

	// With nil guard, tasks should succeed
	if !result.Success {
		t.Errorf("expected task to succeed with nil guard, got errors: %v", result.Errors)
	}
}

// TestSafetyGuardWithAllAdapters tests that all adapter types respect the guard
func TestSafetyGuardWithAllAdapters(t *testing.T) {
	tempDir := t.TempDir()

	safetyCfg := config.SafetyConfig{
		AllowedPaths:    []string{tempDir},
		BlockedCommands: []string{"dangerous"},
		MaxFileSize:     10 * 1024 * 1024,
	}

	guard, err := safety.NewGuard(safetyCfg, tempDir)
	if err != nil {
		t.Fatalf("failed to create guard: %v", err)
	}

	adapters := map[string]Adapter{
		"claude":   NewClaudeAdapter("echo", []string{}, tempDir, guard),
		"codex":    NewCodexAdapter("echo", []string{}, tempDir, guard),
		"opencode": NewOpenCodeAdapter("echo", []string{}, tempDir, guard),
		"exec":     NewExecAdapter(tempDir, guard),
		"kimi":     NewKimiAdapter("echo", []string{}, tempDir, guard),
		"gemini":   NewGeminiAdapter("echo", []string{}, tempDir, guard),
	}

	for name, adap := range adapters {
		t.Run(name, func(t *testing.T) {
			worker := adap.CreateWorker(name + "-test")

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			testTask := &task.Task{
				ID:          name + "-guard-task",
				Type:        task.TypeCode,
				Title:       "Guard Test",
				Description: "dangerous command should be blocked",
			}

			// For exec adapter, the description IS the command
			if name == "exec" {
				testTask.Context = map[string]string{"command": "dangerous operation"}
			}

			err := worker.Spawn(ctx, testTask)
			if err != nil {
				t.Fatalf("failed to spawn worker: %v", err)
			}

			// Wait for completion
			for i := 0; i < 20; i++ {
				status := worker.Monitor()
				if status == "failed" || status == "complete" {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}

			result := worker.Result()
			if result == nil {
				t.Fatal("expected result but got nil")
			}

			// Should fail due to blocked command
			if result.Success {
				t.Error("expected task to fail due to blocked command")
			}
		})
	}
}

// Helper function
func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > len(substr) && indexOfStr(s, substr) >= 0))
}

func indexOfStr(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
