package adapter

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/exedev/queen-bee/internal/task"
	"github.com/exedev/queen-bee/internal/worker"
)

// GeminiAdapter wraps the `gemini` CLI
type GeminiAdapter struct {
	command string
	args    []string
	workDir string
}

func NewGeminiAdapter(command string, args []string, workDir string) *GeminiAdapter {
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
}

func (w *GeminiWorker) ID() string   { return w.id }
func (w *GeminiWorker) Type() string { return "gemini" }

func (w *GeminiWorker) Spawn(ctx context.Context, t *task.Task) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	prompt := buildPrompt(t)

	args := make([]string, len(w.adapter.args))
	copy(args, w.adapter.args)

	w.cmd = exec.CommandContext(ctx, w.adapter.command, args...)
	if w.adapter.workDir != "" {
		w.cmd.Dir = w.adapter.workDir
	}

	// Pipe prompt via stdin to avoid shell escaping issues with long prompts
	w.cmd.Stdin = strings.NewReader(prompt)

	var stdout, stderr bytes.Buffer
	w.cmd.Stdout = &stdout
	w.cmd.Stderr = &stderr

	w.status = worker.StatusRunning

	go func() {
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
			w.result = &task.Result{
				Success: false,
				Output:  stdout.String(),
				Errors:  []string{err.Error(), stderr.String()},
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
