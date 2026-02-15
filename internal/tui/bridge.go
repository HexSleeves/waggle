package tui

import (
	"fmt"
	"io"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// Program wraps a Bubble Tea program with helper methods for sending events.
// Messages sent before Run() are buffered and replayed once the program starts.
type Program struct {
	program     *tea.Program
	mu          sync.Mutex
	started     bool
	buffer      []tea.Msg
	quiet       bool        // Quiet mode: don't start TUI, print only essentials
	objectiveCh chan string // receives objective in interactive mode
}

// NewProgram creates a TUI program with a pre-set objective.
func NewProgram(objective string, maxTurns int) *Program {
	model := New(objective, maxTurns)
	p := tea.NewProgram(model, tea.WithAltScreen())
	return &Program{program: p}
}

// NewInteractiveProgram creates a TUI that prompts for an objective.
// The objective is sent to the returned channel when the user presses Enter.
func NewInteractiveProgram(maxTurns int) (*Program, <-chan string) {
	ch := make(chan string, 1)
	model := NewInteractive(maxTurns, ch)
	p := tea.NewProgram(model, tea.WithAltScreen())
	prog := &Program{program: p, objectiveCh: ch}
	return prog, ch
}

// SetQuiet enables quiet mode where the TUI is not started and only
// essential messages (task completions/failures) are printed.
func (p *Program) SetQuiet(quiet bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.quiet = quiet
}

// Run starts the TUI (blocking). Replays buffered messages first.
// In quiet mode, returns immediately without starting the TUI.
func (p *Program) Run() (tea.Model, error) {
	p.mu.Lock()
	if p.quiet {
		p.mu.Unlock()
		return Model{done: true, success: true}, nil
	}
	// Mark as started and flush buffer
	p.started = true
	buffered := make([]tea.Msg, len(p.buffer))
	copy(buffered, p.buffer)
	p.buffer = nil
	p.mu.Unlock()

	// Replay buffered messages in a goroutine (Run must be called first)
	go func() {
		for _, msg := range buffered {
			p.program.Send(msg)
		}
	}()

	return p.program.Run()
}

// Send sends a message to the TUI. Buffers if the program hasn't started yet.
// In quiet mode, essential messages are printed directly to stdout.
func (p *Program) Send(msg tea.Msg) {
	p.mu.Lock()
	if p.quiet {
		p.mu.Unlock()
		// In quiet mode, only print essential completion/failure messages
		switch m := msg.(type) {
		case DoneMsg:
			if m.Error != "" {
				fmt.Printf("❌ Failed: %s\n", m.Error)
			} else {
				fmt.Printf("✅ %s\n", m.Summary)
			}
		}
		return
	}
	if !p.started {
		p.buffer = append(p.buffer, msg)
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	p.program.Send(msg)
}

// SendTaskUpdate sends a task status change.
func (p *Program) SendTaskUpdate(id, title, taskType, status, workerID string) {
	p.Send(TaskUpdateMsg{
		ID: id, Title: title, Type: taskType,
		Status: status, WorkerID: workerID,
	})
}

// SendDone sends the completion message.
func (p *Program) SendDone(success bool, summary, errMsg string) {
	p.Send(DoneMsg{Success: success, Summary: summary, Error: errMsg})
}

// SendWorkerOutput sends accumulated worker output to the TUI.
func (p *Program) SendWorkerOutput(workerID, output string) {
	p.Send(WorkerOutputMsg{WorkerID: workerID, Output: output})
}

// LogWriter returns an io.Writer that sends each line to the TUI as a LogMsg.
// Use this as the output for log.New() to capture the Queen's logger output.
func (p *Program) LogWriter() io.Writer {
	return &tuiWriter{p: p}
}

type tuiWriter struct {
	p   *Program
	buf []byte
}

func (w *tuiWriter) Write(data []byte) (int, error) {
	w.buf = append(w.buf, data...)
	for {
		nl := strings.IndexByte(string(w.buf), '\n')
		if nl == -1 {
			break
		}
		line := string(w.buf[:nl])
		w.buf = w.buf[nl+1:]

		// Strip the log prefix (date/time)
		line = stripLogPrefix(line)

		if line == "" {
			continue
		}

		// Route to appropriate TUI message based on content
		w.routeLine(line)
	}
	return len(data), nil
}

func (w *tuiWriter) routeLine(line string) {
	// Skip empty/decoration lines
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "━━━") {
		return
	}

	switch {
	case strings.Contains(line, "👑 Queen:"):
		// Extract just the Queen's text
		if idx := strings.Index(line, "👑 Queen: "); idx != -1 {
			text := line[idx+len("👑 Queen: "):]
			w.p.Send(QueenThinkingMsg{Text: text})
		}

	case strings.Contains(line, "🔧 Tool:"):
		if idx := strings.Index(line, "🔧 Tool: "); idx != -1 {
			name := strings.TrimSpace(line[idx+len("🔧 Tool: "):])
			w.p.Send(ToolCallMsg{Name: name})
		}

	case strings.Contains(line, "✓ Result:"):
		if idx := strings.Index(line, "✓ Result: "); idx != -1 {
			result := strings.TrimSpace(line[idx+len("✓ Result: "):])
			w.p.Send(ToolResultMsg{Result: result})
		}

	case strings.Contains(line, "⚠ Tool error:"):
		if idx := strings.Index(line, "⚠ Tool error: "); idx != -1 {
			errText := strings.TrimSpace(line[idx+len("⚠ Tool error: "):])
			w.p.Send(ToolResultMsg{Result: errText, IsError: true})
		}

	case strings.Contains(line, "Agent Turn"):
		var turn, maxTurn int
		if _, err := fmt.Sscanf(trimmed, "Agent Turn %d/%d", &turn, &maxTurn); err == nil && turn > 0 {
			w.p.Send(TurnMsg{Turn: turn, MaxTurn: maxTurn})
		}

	// Filter out noisy/redundant lines
	case strings.Contains(line, "Iteration ") && strings.Contains(line, "Phase:"):
		// skip legacy loop iteration headers
	case strings.HasPrefix(trimmed, "Available adapters:"):
		// skip adapter list (shown in startup)
	case trimmed == "":
		// skip blank lines

	default:
		w.p.Send(LogMsg{Text: trimmed})
	}
}

// stripLogPrefix removes Go log prefixes like "2026/02/14 20:30:59 " or timestamps.
func stripLogPrefix(line string) string {
	// Standard: "2006/01/02 15:04:05 msg"
	if len(line) >= 20 && line[4] == '/' && line[7] == '/' && line[10] == ' ' &&
		line[13] == ':' && line[16] == ':' {
		// Check if microseconds follow: "2006/01/02 15:04:05.000000 msg"
		if len(line) > 26 && line[19] == '.' {
			// Find the space after the microseconds
			for i := 20; i < len(line) && i < 30; i++ {
				if line[i] == ' ' {
					return strings.TrimSpace(line[i+1:])
				}
			}
		}
		return strings.TrimSpace(line[20:])
	}
	// Tagged: "[TAG] 2006/01/02 ..."
	if len(line) > 0 && line[0] == '[' {
		if idx := strings.Index(line, "] "); idx != -1 && idx < 20 {
			return stripLogPrefix(line[idx+2:])
		}
	}
	// Bare timestamp at start: "20:30:59 msg" (time only)
	if len(line) >= 9 && line[2] == ':' && line[5] == ':' && line[8] == ' ' {
		return strings.TrimSpace(line[9:])
	}
	return strings.TrimSpace(line)
}
