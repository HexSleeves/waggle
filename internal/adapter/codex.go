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

// CodexAdapter wraps the `codex` CLI
type CodexAdapter struct {
	command string
	args    []string
	workDir string
	guard   *safety.Guard
}

func NewCodexAdapter(command string, args []string, workDir string, guard *safety.Guard) *CodexAdapter {
	if command == "" {
		command = "codex"
	}
	return &CodexAdapter{
		command: command,
		args:    args,
		workDir: workDir,
		guard:   guard,
	}
}

func (a *CodexAdapter) Name() string { return "codex" }

func (a *CodexAdapter) Available() bool {
	_, err := exec.LookPath(a.command)
	return err == nil
}

func (a *CodexAdapter) CreateWorker(id string) worker.Bee {
	return &CodexWorker{
		id:      id,
		adapter: a,
		status:  worker.StatusIdle,
		guard:   a.guard,
	}
}

// CodexWorker is a Bee backed by the codex CLI
type CodexWorker struct {
	id      string
	adapter *CodexAdapter
	status  worker.Status
	result  *task.Result
	output  strings.Builder
	cmd     *exec.Cmd
	mu      sync.Mutex
	guard   *safety.Guard
}

func (w *CodexWorker) ID() string   { return w.id }
func (w *CodexWorker) Type() string { return "codex" }

func (w *CodexWorker) Spawn(ctx context.Context, t *task.Task) error {
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
			return nil
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

	// codex exec "prompt"
	args := make([]string, len(w.adapter.args))
	copy(args, w.adapter.args)
	args = append(args, prompt)

	w.cmd = exec.CommandContext(ctx, w.adapter.command, args...)
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
			errMsg := err.Error()
			if errType == errors.ErrorTypeRetryable {
				errMsg = fmt.Sprintf("[retryable] %s", err.Error())
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

func (w *CodexWorker) Monitor() worker.Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *CodexWorker) Result() *task.Result {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.result
}

func (w *CodexWorker) Kill() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cmd != nil && w.cmd.Process != nil {
		w.status = worker.StatusFailed
		return w.cmd.Process.Kill()
	}
	return nil
}

func (w *CodexWorker) Output() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.output.String()
}
