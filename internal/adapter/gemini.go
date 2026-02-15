package adapter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/exedev/waggle/internal/errors"
	"github.com/exedev/waggle/internal/safety"
	"github.com/exedev/waggle/internal/task"
	"github.com/exedev/waggle/internal/worker"
)

// GeminiAdapter wraps the `gemini` CLI
type GeminiAdapter struct {
	command string
	args    []string
	workDir string
	guard   *safety.Guard
}

func NewGeminiAdapter(command string, args []string, workDir string, guard *safety.Guard) *GeminiAdapter {
	if command == "" {
		command = "gemini"
	}
	// If command isn't on PATH, check common install locations
	if _, err := exec.LookPath(command); err != nil {
		for _, p := range []string{
			os.ExpandEnv("$HOME/.bun/bin/gemini"),
			"/usr/local/bin/gemini",
		} {
			if _, err := os.Stat(p); err == nil {
				command = p
				break
			}
		}
	}
	return &GeminiAdapter{
		command: command,
		args:    args,
		workDir: workDir,
		guard:   guard,
	}
}

func (a *GeminiAdapter) Name() string { return "gemini" }

func (a *GeminiAdapter) Available() bool {
	// Check both LookPath and direct file existence (for absolute paths)
	if _, err := exec.LookPath(a.command); err == nil {
		return true
	}
	if _, err := os.Stat(a.command); err == nil {
		return true
	}
	return false
}

func (a *GeminiAdapter) CreateWorker(id string) worker.Bee {
	return &GeminiWorker{
		id:      id,
		adapter: a,
		status:  worker.StatusIdle,
		guard:   a.guard,
	}
}

// GeminiWorker is a Bee backed by the gemini CLI
type GeminiWorker struct {
	id      string
	adapter *GeminiAdapter
	status  worker.Status
	result  *task.Result
	output  strings.Builder
	cmd     *exec.Cmd
	mu      sync.Mutex
	guard   *safety.Guard
}

func (w *GeminiWorker) ID() string   { return w.id }
func (w *GeminiWorker) Type() string { return "gemini" }

func (w *GeminiWorker) Spawn(ctx context.Context, t *task.Task) error {
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

	args := make([]string, len(w.adapter.args))
	copy(args, w.adapter.args)

	w.cmd = exec.CommandContext(ctx, w.adapter.command, args...)
	if w.adapter.workDir != "" {
		w.cmd.Dir = w.adapter.workDir
	}

	// Pipe prompt via stdin to avoid shell escaping issues with long prompts
	w.cmd.Stdin = strings.NewReader(prompt)

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

func (w *GeminiWorker) Monitor() worker.Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *GeminiWorker) Result() *task.Result {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.result
}

func (w *GeminiWorker) Kill() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cmd != nil && w.cmd.Process != nil {
		w.status = worker.StatusFailed
		return w.cmd.Process.Kill()
	}
	return nil
}

func (w *GeminiWorker) Output() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.output.String()
}
