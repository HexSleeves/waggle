package output

import (
	"io"
	"os"
)

// Mode represents the output mode.
type Mode int

const (
	// ModeTUI is the interactive terminal UI mode.
	ModeTUI Mode = iota
	// ModePlain is the plain text log mode.
	ModePlain
	// ModeJSON is the structured JSON output mode.
	ModeJSON
	// ModeQuiet suppresses most output.
	ModeQuiet
)

// Manager handles output based on the selected mode.
type Manager struct {
	mode       Mode
	jsonWriter *JSONWriter
	stdout     io.Writer
	stderr     io.Writer
}

// NewManager creates a new output manager.
func NewManager(mode Mode) *Manager {
	return &Manager{
		mode:   mode,
		stdout: os.Stdout,
		stderr: os.Stderr,
	}
}

// NewManagerWithWriters creates a new output manager with custom writers (for testing).
func NewManagerWithWriters(mode Mode, stdout, stderr io.Writer) *Manager {
	return &Manager{
		mode:   mode,
		stdout: stdout,
		stderr: stderr,
	}
}

// Mode returns the current output mode.
func (m *Manager) Mode() Mode {
	return m.mode
}

// IsJSON returns true if JSON mode is enabled.
func (m *Manager) IsJSON() bool {
	return m.mode == ModeJSON
}

// IsTUI returns true if TUI mode is enabled.
func (m *Manager) IsTUI() bool {
	return m.mode == ModeTUI
}

// IsPlain returns true if plain mode is enabled.
func (m *Manager) IsPlain() bool {
	return m.mode == ModePlain
}

// IsQuiet returns true if quiet mode is enabled.
func (m *Manager) IsQuiet() bool {
	return m.mode == ModeQuiet
}

// SetJSONWriter sets the JSON writer (call after session creation).
func (m *Manager) SetJSONWriter(jw *JSONWriter) {
	m.jsonWriter = jw
}

// JSONWriter returns the JSON writer (may be nil).
func (m *Manager) JSONWriter() *JSONWriter {
	return m.jsonWriter
}

// Stdout returns the stdout writer.
func (m *Manager) Stdout() io.Writer {
	return m.stdout
}

// Stderr returns the stderr writer.
func (m *Manager) Stderr() io.Writer {
	return m.stderr
}

// Printf writes formatted output (no-op in JSON/quiet mode unless to stderr).
func (m *Manager) Printf(format string, args ...interface{}) {
	if m.mode == ModeJSON || m.mode == ModeQuiet {
		return
	}
	// Printf implementation would go here if needed
}

// Println writes a line (no-op in JSON/quiet mode).
func (m *Manager) Println(args ...interface{}) {
	if m.mode == ModeJSON || m.mode == ModeQuiet {
		return
	}
	// Println implementation would go here if needed
}
