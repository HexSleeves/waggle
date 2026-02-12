package adapter

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"sync"

	"github.com/exedev/queen-bee/internal/task"
	"github.com/exedev/queen-bee/internal/worker"
)

// OpenCodeAdapter wraps the `opencode` CLI
type OpenCodeAdapter struct {
	command string
	args    []string
	workDir string
}

func NewOpenCodeAdapter(command string, args []string, workDir string) *OpenCodeAdapter {
	if command == "" {
		command = "opencode"
	}
	return &OpenCodeAdapter{
		command: command,
		args:    args,
		workDir: workDir,
	}
}

func (a *OpenCodeAdapter) Name() string { return "opencode" }

func (a *OpenCodeAdapter) Available() bool {
	_, err := exec.LookPath(a.command)
	return err == nil
}

func (a *OpenCodeAdapter) CreateWorker(id string) worker.Bee {
	return &OpenCodeWorker{
		id:      id,
		adapter: a,
		status:  worker.StatusIdle,
	}
}

// OpenCodeWorker is a Bee backed by the opencode CLI
type OpenCodeWorker struct {
	id      string
	adapter *OpenCodeAdapter
	status  worker.Status
	result  *task.Result
	output  strings.Builder
	cmd     *exec.Cmd
	mu      sync.Mutex
}

func (w *OpenCodeWorker) ID() string   { return w.id }
func (w *OpenCodeWorker) Type() string { return "opencode" }

func (w *OpenCodeWorker) Spawn(ctx context.Context, t *task.Task) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	prompt := buildPrompt(t)
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

func (w *OpenCodeWorker) Monitor() worker.Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *OpenCodeWorker) Result() *task.Result {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.result
}

func (w *OpenCodeWorker) Kill() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cmd != nil && w.cmd.Process != nil {
		w.status = worker.StatusFailed
		return w.cmd.Process.Kill()
	}
	return nil
}

func (w *OpenCodeWorker) Output() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.output.String()
}
