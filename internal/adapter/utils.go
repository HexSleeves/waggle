package adapter

import (
	"errors"
	"os/exec"
	"strings"
	"sync"
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

// streamWriter is a thread-safe io.Writer that appends to a strings.Builder.
// It allows worker output to be read live via Output() while the process runs.
type streamWriter struct {
	mu  *sync.Mutex
	buf *strings.Builder
}

func newStreamWriter(mu *sync.Mutex, buf *strings.Builder) *streamWriter {
	return &streamWriter{mu: mu, buf: buf}
}

func (sw *streamWriter) Write(p []byte) (int, error) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return sw.buf.Write(p)
}
