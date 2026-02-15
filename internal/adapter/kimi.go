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

// KimiAdapter wraps the `kimi` CLI (kimi-code)
type KimiAdapter struct {
	command string
	args    []string
	workDir string
	guard   *safety.Guard
}

func NewKimiAdapter(command string, args []string, workDir string, guard *safety.Guard) *KimiAdapter {
	if command == "" {
		command = "kimi"
	}
	// Resolve if not on PATH
	if _, err := exec.LookPath(command); err != nil {
		for _, p := range []string{
			os.ExpandEnv("$HOME/.local/bin/kimi"),
			"/usr/local/bin/kimi",
		} {
			if _, err := os.Stat(p); err == nil {
				command = p
				break
			}
		}
	}
	if len(args) == 0 {
		args = []string{"--print", "--final-message-only", "-p"}
	}
	return &KimiAdapter{
		command: command,
		args:    args,
		workDir: workDir,
		guard:   guard,
	}
}

func (a *KimiAdapter) Name() string { return "kimi" }

func (a *KimiAdapter) Available() bool {
	if _, err := exec.LookPath(a.command); err == nil {
		return true
	}
	if _, err := os.Stat(a.command); err == nil {
		return true
	}
	return false
}

func (a *KimiAdapter) CreateWorker(id string) worker.Bee {
	return &KimiWorker{
		id:      id,
		adapter: a,
		status:  worker.StatusIdle,
		guard:   a.guard,
	}
}

// KimiWorker is a Bee backed by the kimi CLI
type KimiWorker struct {
	id      string
	adapter *KimiAdapter
	status  worker.Status
	result  *task.Result
	output  strings.Builder
	cmd     *exec.Cmd
	mu      sync.Mutex
	guard   *safety.Guard
}

func (w *KimiWorker) ID() string   { return w.id }
func (w *KimiWorker) Type() string { return "kimi" }

func (w *KimiWorker) Spawn(ctx context.Context, t *task.Task) error {
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

	// kimi --print --final-message-only -p "prompt"
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

func (w *KimiWorker) Monitor() worker.Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *KimiWorker) Result() *task.Result {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.result
}

func (w *KimiWorker) Kill() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cmd != nil && w.cmd.Process != nil {
		w.status = worker.StatusFailed
		return w.cmd.Process.Kill()
	}
	return nil
}

func (w *KimiWorker) Output() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.output.String()
}
