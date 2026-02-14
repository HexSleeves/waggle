package adapter

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/exedev/queen-bee/internal/errors"
	"github.com/exedev/queen-bee/internal/safety"
	"github.com/exedev/queen-bee/internal/task"
	"github.com/exedev/queen-bee/internal/worker"
)

// ClaudeAdapter wraps the `claude` CLI
type ClaudeAdapter struct {
	command string
	args    []string
	workDir string
	guard   *safety.Guard
}

func NewClaudeAdapter(command string, args []string, workDir string, guard *safety.Guard) *ClaudeAdapter {
	if command == "" {
		command = "claude"
	}
	if len(args) == 0 {
		args = []string{"-p"}
	}
	return &ClaudeAdapter{
		command: command,
		args:    args,
		workDir: workDir,
		guard:   guard,
	}
}

func (a *ClaudeAdapter) Name() string { return "claude-code" }

func (a *ClaudeAdapter) Available() bool {
	_, err := exec.LookPath(a.command)
	return err == nil
}

func (a *ClaudeAdapter) CreateWorker(id string) worker.Bee {
	return &ClaudeWorker{
		id:      id,
		adapter: a,
		status:  worker.StatusIdle,
		guard:   a.guard,
	}
}

// ClaudeWorker is a Bee backed by the claude CLI
type ClaudeWorker struct {
	id      string
	adapter *ClaudeAdapter
	status  worker.Status
	result  *task.Result
	output  strings.Builder
	cmd     *exec.Cmd
	mu      sync.Mutex
	guard   *safety.Guard
}

func (w *ClaudeWorker) ID() string   { return w.id }
func (w *ClaudeWorker) Type() string { return "claude-code" }

func (w *ClaudeWorker) Spawn(ctx context.Context, t *task.Task) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Safety check: validate task paths if guard is configured
	if w.guard != nil {
		if err := w.guard.ValidateTaskPaths(t.AllowedPaths); err != nil {
			w.status = worker.StatusFailed
			w.result = &task.Result{
				Success: false,
				Errors:  []string{fmt.Sprintf("safety check failed: %v", err)},
			}
			return nil // Return nil to match async behavior
		}

		// Safety check: check for blocked commands in task description
		if err := w.guard.CheckCommand(t.Description); err != nil {
			w.status = worker.StatusFailed
			w.result = &task.Result{
				Success: false,
				Errors:  []string{fmt.Sprintf("safety check failed: %v", err)},
			}
			return nil
		}

		// Add read-only mode warning to prompt if enabled
		if w.guard.IsReadOnly() {
			t.Description = "[SAFETY WARNING: System is in read-only mode]\n\n" + t.Description
		}
	}

	prompt := buildPrompt(t)

	// Build command: claude --print "prompt"
	args := make([]string, len(w.adapter.args))
	copy(args, w.adapter.args)
	args = append(args, prompt)

	w.cmd = exec.CommandContext(ctx, w.adapter.command, args...)
	if w.adapter.workDir != "" {
		w.cmd.Dir = w.adapter.workDir
	}

	var stdout, stderr bytes.Buffer
	w.cmd.Stdout = &stdout
	w.cmd.Stderr = &stderr

	w.status = worker.StatusRunning

	// Run async with panic recovery
	go func() {
		defer func() {
			if r := recover(); r != nil {
				recovery := errors.RecoverPanic(r)
				w.mu.Lock()
				defer w.mu.Unlock()
				w.status = worker.StatusFailed
				w.result = &task.Result{
					Success: false,
					Output:  w.output.String(),
					Errors:  []string{recovery.ErrorMsg},
				}
			}
		}()

		err := w.cmd.Run()

		w.mu.Lock()
		defer w.mu.Unlock()

		w.output.WriteString(stdout.String())
		if stderr.Len() > 0 {
			w.output.WriteString("\n[STDERR]\n")
			w.output.WriteString(stderr.String())
		}

		if err != nil {
			w.status = worker.StatusFailed
			// Classify error for retry decisions
			errType := errors.ClassifyErrorWithExitCode(err, getExitCode(err))
			errMsg := err.Error()
			if errType == errors.ErrorTypeRetryable {
				errMsg = fmt.Sprintf("[retryable] %s", err.Error())
			}
			w.result = &task.Result{
				Success: false,
				Output:  stdout.String(),
				Errors:  []string{errMsg, stderr.String()},
			}
		} else {
			w.status = worker.StatusComplete
			w.result = &task.Result{
				Success: true,
				Output:  stdout.String(),
			}
		}
	}()

	return nil
}

func (w *ClaudeWorker) Monitor() worker.Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *ClaudeWorker) Result() *task.Result {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.result
}

func (w *ClaudeWorker) Kill() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cmd != nil && w.cmd.Process != nil {
		w.status = worker.StatusFailed
		return w.cmd.Process.Kill()
	}
	return nil
}

func (w *ClaudeWorker) Output() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.output.String()
}

func buildPrompt(t *task.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Task: %s\n", t.Title)
	fmt.Fprintf(&b, "Type: %s\n", t.Type)
	fmt.Fprintf(&b, "Description:\n%s\n", t.Description)

	if len(t.Context) > 0 {
		fmt.Fprintf(&b, "\nContext:\n")
		for k, v := range t.Context {
			fmt.Fprintf(&b, "- %s: %s\n", k, v)
		}
	}

	if len(t.AllowedPaths) > 0 {
		fmt.Fprintf(&b, "\nOnly modify files in: %s\n", strings.Join(t.AllowedPaths, ", "))
	}

	// Scope constraints — tell the worker what NOT to do
	if len(t.Constraints) > 0 {
		fmt.Fprintf(&b, "\n--- SCOPE CONSTRAINTS (you MUST follow these) ---\n")
		for _, c := range t.Constraints {
			fmt.Fprintf(&b, "• %s\n", c)
		}
		fmt.Fprintf(&b, "--- END CONSTRAINTS ---\n")
	}

	return b.String()
}
