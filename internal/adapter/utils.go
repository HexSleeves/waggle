package adapter

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/exedev/waggle/internal/task"
)

// getExitCode extracts the exit code from an error.
// Returns -1 if the error is nil or doesn't have an exit code.
func getExitCode(err error) int {
	if err == nil {
		return 0
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}

	return -1
}

const truncationMarker = "\n\n[output truncated — exceeded max output size]\n"

// streamWriter is a thread-safe io.Writer that appends to a strings.Builder.
// It allows worker output to be read live via Output() while the process runs.
// If maxSize > 0, output is capped and a truncation marker is appended.
type streamWriter struct {
	mu        *sync.Mutex
	buf       *strings.Builder
	maxSize   int
	truncated bool
}

func newStreamWriter(mu *sync.Mutex, buf *strings.Builder, maxSize int) *streamWriter {
	return &streamWriter{mu: mu, buf: buf, maxSize: maxSize}
}

func (sw *streamWriter) Write(p []byte) (int, error) {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	if sw.maxSize > 0 && sw.truncated {
		// Already truncated — silently discard but report full length written
		return len(p), nil
	}

	if sw.maxSize > 0 {
		remaining := sw.maxSize - sw.buf.Len()
		if len(p) > remaining {
			if remaining > 0 {
				sw.buf.Write(p[:remaining])
			}
			sw.buf.WriteString(truncationMarker)
			sw.truncated = true
			return len(p), nil
		}
	}

	return sw.buf.Write(p)
}

// buildPrompt constructs the prompt string sent to a worker CLI.
func buildPrompt(t *task.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Task: %s\n", t.Title)
	fmt.Fprintf(&b, "Type: %s\n", t.Type)
	fmt.Fprintf(&b, "Description:\n%s\n", t.GetDescription())

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
	constraints := t.GetConstraints()
	if len(constraints) > 0 {
		fmt.Fprintf(&b, "\n--- SCOPE CONSTRAINTS (you MUST follow these) ---\n")
		for _, c := range constraints {
			fmt.Fprintf(&b, "• %s\n", c)
		}
		fmt.Fprintf(&b, "--- END CONSTRAINTS ---\n")
	}

	return b.String()
}
