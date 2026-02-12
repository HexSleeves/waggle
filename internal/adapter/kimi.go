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

// KimiAdapter wraps the `kimi` CLI (kimi-code)
type KimiAdapter struct {
	command string
	args    []string
	workDir string
}

func NewKimiAdapter(command string, args []string, workDir string) *KimiAdapter {
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
}

func (w *KimiWorker) ID() string   { return w.id }
func (w *KimiWorker) Type() string { return "kimi" }

func (w *KimiWorker) Spawn(ctx context.Context, t *task.Task) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	prompt := buildPrompt(t)

	// kimi --print --final-message-only -p "prompt"
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
