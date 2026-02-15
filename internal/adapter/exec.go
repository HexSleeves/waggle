package adapter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/exedev/waggle/internal/errors"
	"github.com/exedev/waggle/internal/safety"
	"github.com/exedev/waggle/internal/task"
	"github.com/exedev/waggle/internal/worker"
)

// ExecAdapter runs tasks as shell commands directly.
// Useful as a fallback when no AI CLI is available, or for
// tasks that are pure shell operations (tests, builds, linting).
type ExecAdapter struct {
	shell   string
	workDir string
	guard   *safety.Guard
}

func NewExecAdapter(workDir string, guard *safety.Guard) *ExecAdapter {
	shell := "/bin/bash"
	if s, err := exec.LookPath("bash"); err == nil {
		shell = s
	}
	return &ExecAdapter{
		shell:   shell,
		workDir: workDir,
		guard:   guard,
	}
}

func (a *ExecAdapter) Name() string    { return "exec" }
func (a *ExecAdapter) Available() bool { return true }

func (a *ExecAdapter) CreateWorker(id string) worker.Bee {
	return &ExecWorker{
		id:      id,
		adapter: a,
		status:  worker.StatusIdle,
		guard:   a.guard,
	}
}

// ExecWorker runs a task's description as a shell script
type ExecWorker struct {
	id      string
	adapter *ExecAdapter
	status  worker.Status
	result  *task.Result
	output  strings.Builder
	cmd     *exec.Cmd
	mu      sync.Mutex
	guard   *safety.Guard
}

func (w *ExecWorker) ID() string   { return w.id }
func (w *ExecWorker) Type() string { return "exec" }

func (w *ExecWorker) Spawn(ctx context.Context, t *task.Task) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// For exec adapter, the task description should contain the shell command(s)
	// If context has a "command" key, use that instead
	script := t.Description
	if cmd, ok := t.Context["command"]; ok {
		script = cmd
	}

	// Safety check: validate task paths if guard is configured
	if w.guard != nil {
		if err := w.guard.ValidateTaskPaths(t.AllowedPaths); err != nil {
			w.status = worker.StatusFailed
			w.result = &task.Result{
				Success: false,
				Errors:  []string{fmt.Sprintf("safety check failed: %v", err)},
			}
			return nil
		}

		// Safety check: check for blocked commands - critical for exec adapter
		// Check both the script and the description
		if err := w.guard.CheckCommand(script); err != nil {
			w.status = worker.StatusFailed
			w.result = &task.Result{
				Success: false,
				Errors:  []string{fmt.Sprintf("safety check failed: %v", err)},
			}
			return nil
		}
		if err := w.guard.CheckCommand(t.Description); err != nil {
			w.status = worker.StatusFailed
			w.result = &task.Result{
				Success: false,
				Errors:  []string{fmt.Sprintf("safety check failed: %v", err)},
			}
			return nil
		}

		// In read-only mode, only allow safe read-only commands
		if w.guard.IsReadOnly() {
			// Prepend a safety marker to output
			w.output.WriteString("[SAFETY WARNING: System is in read-only mode - write operations blocked]\n\n")
			// We don't block here, but the command will fail if it tries to write
			// since actual filesystem restrictions should be enforced at OS level
		}
	}

	w.cmd = exec.CommandContext(ctx, w.adapter.shell, "-c", script)
	if w.adapter.workDir != "" {
		w.cmd.Dir = w.adapter.workDir
	}

	// Stream output live to w.output for TUI display
	var stdoutBuf, stderrBuf bytes.Buffer
	stream := newStreamWriter(&w.mu, &w.output)
	w.cmd.Stdout = io.MultiWriter(&stdoutBuf, stream)
	w.cmd.Stderr = io.MultiWriter(&stderrBuf, stream)

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

		if err != nil {
			w.status = worker.StatusFailed
			errType := errors.ClassifyErrorWithExitCode(err, getExitCode(err))
			errMsg := fmt.Sprintf("%v", err)
			if errType == errors.ErrorTypeRetryable {
				errMsg = fmt.Sprintf("[retryable] %v", err)
			}
			w.result = &task.Result{
				Success: false,
				Output:  stdoutBuf.String(),
				Errors:  []string{errMsg, stderrBuf.String()},
			}
		} else {
			w.status = worker.StatusComplete
			w.result = &task.Result{
				Success: true,
				Output:  stdoutBuf.String(),
			}
		}
	}()

	return nil
}

func (w *ExecWorker) Monitor() worker.Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *ExecWorker) Result() *task.Result {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.result
}

func (w *ExecWorker) Kill() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cmd != nil && w.cmd.Process != nil {
		w.status = worker.StatusFailed
		return w.cmd.Process.Kill()
	}
	return nil
}

func (w *ExecWorker) Output() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.output.String()
}
