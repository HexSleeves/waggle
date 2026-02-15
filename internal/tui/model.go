package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	maxQueenLines  = 200
	maxLogLines    = 100
	tickInterval   = time.Second
)

// TaskInfo tracks task state for display.
type TaskInfo struct {
	ID       string
	Title    string
	Type     string
	Status   string
	WorkerID string
	Order    int // insertion order
}

// WorkerInfo tracks worker state for display.
type WorkerInfo struct {
	ID      string
	TaskID  string
	Status  string
	Adapter string
	Started time.Time
}

// Model is the Bubble Tea model for the Waggle TUI.
type Model struct {
	// Content
	objective   string
	queenLines  []queenLine // Queen panel lines
	tasks       []TaskInfo  // ordered task list
	taskMap     map[string]int // task ID -> index in tasks slice
	workers     map[string]*WorkerInfo

	// State
	turn        int
	maxTurn     int
	startTime   time.Time
	done        bool
	success     bool
	finalMsg    string

	// UI state
	width       int
	height      int
	queenScroll int // scroll offset for queen panel (from bottom)

	// For tick
	quitting    bool
}

type queenLine struct {
	text  string
	style string // "think", "tool", "result", "error", "info"
}

// New creates a new TUI model.
func New(objective string, maxTurns int) Model {
	return Model{
		objective:  objective,
		queenLines: []queenLine{},
		tasks:      []TaskInfo{},
		taskMap:    make(map[string]int),
		workers:    make(map[string]*WorkerInfo),
		maxTurn:    maxTurns,
		startTime:  time.Now(),
	}
}

func (m Model) Init() tea.Cmd {
	return tickCmd()
}

func tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg {
		return TickMsg{}
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.queenScroll < len(m.queenLines)-1 {
				m.queenScroll++
			}
		case "down", "j":
			if m.queenScroll > 0 {
				m.queenScroll--
			}
		default:
			// Any key quits after done
			if m.done {
				m.quitting = true
				return m, tea.Quit
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case TickMsg:
		if !m.done {
			return m, tickCmd()
		}

	case QueenThinkingMsg:
		m.addQueenLine(msg.Text, "think")
		m.queenScroll = 0 // auto-scroll to bottom

	case ToolCallMsg:
		line := "→ " + msg.Name
		if msg.Input != "" {
			input := msg.Input
			if len(input) > 80 {
				input = input[:80] + "..."
			}
			line += "(" + input + ")"
		}
		m.addQueenLine(line, "tool")
		m.queenScroll = 0

	case ToolResultMsg:
		prefix := "✓ "
		style := "result"
		if msg.IsError {
			prefix = "✗ "
			style = "error"
		}
		result := msg.Result
		if len(result) > 120 {
			result = result[:120] + "..."
		}
		m.addQueenLine(prefix+msg.Name+": "+result, style)
		m.queenScroll = 0

	case TaskUpdateMsg:
		m.updateTask(msg)

	case WorkerUpdateMsg:
		m.updateWorker(msg)

	case TurnMsg:
		m.turn = msg.Turn
		m.maxTurn = msg.MaxTurn
		m.addQueenLine("", "info") // blank separator

	case DoneMsg:
		m.done = true
		m.success = msg.Success
		if msg.Error != "" {
			m.finalMsg = msg.Error
			m.addQueenLine("❌ "+msg.Error, "error")
		} else {
			m.finalMsg = msg.Summary
			m.addQueenLine("✅ "+msg.Summary, "info")
		}
		// Give user a moment to see the result, then quit
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return tea.KeyMsg{}
		})

	case LogMsg:
		m.addQueenLine(msg.Text, "info")
		m.queenScroll = 0
	}

	return m, nil
}

func (m *Model) addQueenLine(text, style string) {
	m.queenLines = append(m.queenLines, queenLine{text: text, style: style})
	if len(m.queenLines) > maxQueenLines {
		m.queenLines = m.queenLines[len(m.queenLines)-maxQueenLines:]
	}
}

func (m *Model) updateTask(msg TaskUpdateMsg) {
	if idx, ok := m.taskMap[msg.ID]; ok {
		m.tasks[idx].Status = msg.Status
		if msg.WorkerID != "" {
			m.tasks[idx].WorkerID = msg.WorkerID
		}
	} else {
		m.taskMap[msg.ID] = len(m.tasks)
		m.tasks = append(m.tasks, TaskInfo{
			ID:       msg.ID,
			Title:    msg.Title,
			Type:     msg.Type,
			Status:   msg.Status,
			WorkerID: msg.WorkerID,
			Order:    len(m.tasks),
		})
	}
}

func (m *Model) updateWorker(msg WorkerUpdateMsg) {
	if msg.Status == "done" || msg.Status == "failed" {
		delete(m.workers, msg.ID)
		return
	}
	m.workers[msg.ID] = &WorkerInfo{
		ID:      msg.ID,
		TaskID:  msg.TaskID,
		Status:  msg.Status,
		Adapter: msg.Adapter,
		Started: time.Now(),
	}
}
