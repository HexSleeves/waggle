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

// PromptMode controls how the task prompt is passed to the CLI process.
type PromptMode int

const (
	// PromptAsArg appends the prompt as the last command-line argument.
	PromptAsArg PromptMode = iota
	// PromptOnStdin pipes the prompt to the process's stdin.
	PromptOnStdin
	// PromptAsScript uses the task description as a shell script (exec adapter).
	PromptAsScript
)

// CLIAdapter is a generic adapter that wraps any CLI tool.
type CLIAdapter struct {
	name    string
	command string
	args    []string
	workDir string
	guard   *safety.Guard
	mode    PromptMode
}

// CLIAdapterConfig holds the configuration for creating a CLIAdapter.
type CLIAdapterConfig struct {
	Name       string
	Command    string
	Args       []string
	WorkDir    string
	Guard      *safety.Guard
	Mode       PromptMode
	// FallbackPaths are checked if the command isn't on PATH.
	FallbackPaths []string
}

// NewCLIAdapter creates a generic CLI adapter from config.
func NewCLIAdapter(cfg CLIAdapterConfig) *CLIAdapter {
	command := cfg.Command
	// Resolve command if not on PATH
	if _, err := exec.LookPath(command); err != nil {
		for _, p := range cfg.FallbackPaths {
			if _, err := exec.LookPath(p); err == nil {
				command = p
				break
			}
		}
	}
	return &CLIAdapter{
		name:    cfg.Name,
		command: command,
		args:    cfg.Args,
		workDir: cfg.WorkDir,
		guard:   cfg.Guard,
		mode:    cfg.Mode,
	}
}

func (a *CLIAdapter) Name() string { return a.name }

func (a *CLIAdapter) Available() bool {
	_, err := exec.LookPath(a.command)
	return err == nil
}

func (a *CLIAdapter) CreateWorker(id string) worker.Bee {
	return &CLIWorker{
		id:      id,
		adapter: a,
		status:  worker.StatusIdle,
	}
}

// CLIWorker is a generic Bee backed by a CLI process.
type CLIWorker struct {
	id      string
	adapter *CLIAdapter
	status  worker.Status
	result  *task.Result
	output  strings.Builder
	cmd     *exec.Cmd
	mu      sync.Mutex
}

func (w *CLIWorker) ID() string   { return w.id }
func (w *CLIWorker) Type() string { return w.adapter.name }

func (w *CLIWorker) Spawn(ctx context.Context, t *task.Task) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	guard := w.adapter.guard

	// Safety checks
	if guard != nil {
		if err := guard.ValidateTaskPaths(t.AllowedPaths); err != nil {
			return w.failSafety("safety check failed: %v", err)
		}
		if err := guard.CheckCommand(t.Description); err != nil {
			return w.failSafety("safety check failed: %v", err)
		}
		if guard.IsReadOnly() {
			t.Description = "[SAFETY WARNING: System is in read-only mode]\n\n" + t.Description
		}
	}

	// Build command based on prompt mode
	switch w.adapter.mode {
	case PromptAsScript:
		script := t.Description
		if cmd, ok := t.Context["command"]; ok {
			script = cmd
		}
		if guard != nil {
			if err := guard.CheckCommand(script); err != nil {
				return w.failSafety("safety check failed: %v", err)
			}
		}
		w.cmd = exec.CommandContext(ctx, w.adapter.command, "-c", script)

	case PromptOnStdin:
		prompt := buildPrompt(t)
		args := make([]string, len(w.adapter.args))
		copy(args, w.adapter.args)
		w.cmd = exec.CommandContext(ctx, w.adapter.command, args...)
		w.cmd.Stdin = strings.NewReader(prompt)

	default: // PromptAsArg
		prompt := buildPrompt(t)
		args := make([]string, len(w.adapter.args))
		copy(args, w.adapter.args)
		args = append(args, prompt)
		w.cmd = exec.CommandContext(ctx, w.adapter.command, args...)
	}

	if w.adapter.workDir != "" {
		w.cmd.Dir = w.adapter.workDir
	}

	// Stream output live to w.output for TUI display
	var stdoutBuf, stderrBuf bytes.Buffer
	stream := newStreamWriter(&w.mu, &w.output)
	w.cmd.Stdout = io.MultiWriter(&stdoutBuf, stream)
	w.cmd.Stderr = io.MultiWriter(&stderrBuf, stream)

	w.status = worker.StatusRunning

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
			var errMsg string
			if ctx.Err() == context.DeadlineExceeded {
				errMsg = "[timeout] worker killed: exceeded task deadline"
			} else {
				errType := errors.ClassifyErrorWithExitCode(err, getExitCode(err))
				errMsg = err.Error()
				if errType == errors.ErrorTypeRetryable {
					errMsg = fmt.Sprintf("[retryable] %s", err.Error())
				}
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

// failSafety sets the worker to failed with a safety error and returns nil
// (nil return matches async behavior — the failure is visible via Monitor/Result).
func (w *CLIWorker) failSafety(format string, args ...interface{}) error {
	w.status = worker.StatusFailed
	w.result = &task.Result{
		Success: false,
		Errors:  []string{fmt.Sprintf(format, args...)},
	}
	return nil
}

func (w *CLIWorker) Monitor() worker.Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *CLIWorker) Result() *task.Result {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.result
}

func (w *CLIWorker) Kill() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cmd != nil && w.cmd.Process != nil {
		w.status = worker.StatusFailed
		return w.cmd.Process.Kill()
	}
	return nil
}

func (w *CLIWorker) Output() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.output.String()
}
