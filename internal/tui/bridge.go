package tui

import (
	"fmt"
	"io"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// Program wraps a Bubble Tea program with helper methods for sending events.
type Program struct {
	program *tea.Program
	mu      sync.Mutex
}

// NewProgram creates a TUI program.
func NewProgram(objective string, maxTurns int) *Program {
	model := New(objective, maxTurns)
	p := tea.NewProgram(model, tea.WithAltScreen())
	return &Program{program: p}
}

// Run starts the TUI (blocking).
func (p *Program) Run() (tea.Model, error) {
	return p.program.Run()
}

// Send sends a message to the TUI.
func (p *Program) Send(msg tea.Msg) {
	p.program.Send(msg)
}

// SendQueenThinking sends a Queen thinking line.
func (p *Program) SendQueenThinking(text string) {
	p.program.Send(QueenThinkingMsg{Text: text})
}

// SendToolCall sends a tool call event.
func (p *Program) SendToolCall(name, input string) {
	p.program.Send(ToolCallMsg{Name: name, Input: input})
}

// SendToolResult sends a tool result event.
func (p *Program) SendToolResult(name, result string, isError bool) {
	p.program.Send(ToolResultMsg{Name: name, Result: result, IsError: isError})
}

// SendTaskUpdate sends a task status change.
func (p *Program) SendTaskUpdate(id, title, taskType, status, workerID string) {
	p.program.Send(TaskUpdateMsg{
		ID: id, Title: title, Type: taskType,
		Status: status, WorkerID: workerID,
	})
}

// SendTurn sends a turn update.
func (p *Program) SendTurn(turn, maxTurn int) {
	p.program.Send(TurnMsg{Turn: turn, MaxTurn: maxTurn})
}

// SendDone sends the completion message.
func (p *Program) SendDone(success bool, summary, errMsg string) {
	p.program.Send(DoneMsg{Success: success, Summary: summary, Error: errMsg})
}

// SendLog sends a raw log line.
func (p *Program) SendLog(text string) {
	p.program.Send(LogMsg{Text: text})
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
	switch {
	case strings.Contains(line, "👑 Queen:"):
		text := strings.TrimPrefix(line, "👑 Queen: ")
		w.p.SendQueenThinking(text)
	case strings.Contains(line, "🔧 Tool:"):
		name := strings.TrimSpace(strings.TrimPrefix(line, "  🔧 Tool: "))
		w.p.SendToolCall(name, "")
	case strings.Contains(line, "✓ Result:"):
		result := strings.TrimSpace(strings.TrimPrefix(line, "  ✓ Result: "))
		w.p.SendToolResult("", result, false)
	case strings.Contains(line, "⚠ Tool error:"):
		errText := strings.TrimSpace(strings.TrimPrefix(line, "  ⚠ Tool error: "))
		w.p.SendToolResult("", errText, true)
	case strings.Contains(line, "Agent Turn"):
		// Parse turn from "━━━ Agent Turn 3/50 ━━━"
		var turn, maxTurn int
		fmt.Sscanf(line, "%*[^0-9]%d/%d", &turn, &maxTurn)
		if turn > 0 {
			w.p.SendTurn(turn, maxTurn)
		}
	case strings.Contains(line, "✅") || strings.Contains(line, "❌") ||
		strings.Contains(line, "⚠") || strings.Contains(line, "✓") ||
		strings.Contains(line, "🐝") || strings.Contains(line, "⚙"):
		w.p.SendLog(line)
	default:
		w.p.SendLog(line)
	}
}

// stripLogPrefix removes the standard log prefix "2026/02/14 20:30:59 "
func stripLogPrefix(line string) string {
	// Standard log format: "2006/01/02 15:04:05 <message>"
	if len(line) > 20 && line[4] == '/' && line[7] == '/' && line[10] == ' ' {
		return strings.TrimSpace(line[20:])
	}
	// With microseconds: "2006/01/02 15:04:05.000000 <message>"
	if len(line) > 27 && line[4] == '/' && line[7] == '/' && line[19] == '.' {
		return strings.TrimSpace(line[27:])
	}
	// Tagged: "[TEST] 2006/01/02 15:04:05 <message>"
	if strings.HasPrefix(line, "[") {
		if idx := strings.Index(line, "] "); idx != -1 {
			return stripLogPrefix(line[idx+2:])
		}
	}
	return strings.TrimSpace(line)
}
