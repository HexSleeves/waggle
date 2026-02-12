package adapter

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/exedev/queen-bee/internal/task"
	"github.com/exedev/queen-bee/internal/worker"
)

// ClaudeAdapter wraps the `claude` CLI
type ClaudeAdapter struct {
	command string
	args    []string
	workDir string
}

func NewClaudeAdapter(command string, args []string, workDir string) *ClaudeAdapter {
	if command == "" {
		command = "claude"
	}
	if len(args) == 0 {
		args = []string{"--print"}
	}
	return &ClaudeAdapter{
		command: command,
		args:    args,
		workDir: workDir,
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
}

func (w *ClaudeWorker) ID() string   { return w.id }
func (w *ClaudeWorker) Type() string { return "claude-code" }

func (w *ClaudeWorker) Spawn(ctx context.Context, t *task.Task) error {
	w.mu.Lock()
	defer w.mu.Unlock()

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

	// Run async
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
	b.WriteString(fmt.Sprintf("Task: %s\n", t.Title))
	b.WriteString(fmt.Sprintf("Type: %s\n", t.Type))
	b.WriteString(fmt.Sprintf("Description:\n%s\n", t.Description))

	if len(t.Context) > 0 {
		b.WriteString("\nContext:\n")
		for k, v := range t.Context {
			b.WriteString(fmt.Sprintf("- %s: %s\n", k, v))
		}
	}

	if len(t.AllowedPaths) > 0 {
		b.WriteString(fmt.Sprintf("\nOnly modify files in: %s\n", strings.Join(t.AllowedPaths, ", ")))
	}

	return b.String()
}
