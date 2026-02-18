package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	maxQueenLines = 200
	maxLogLines   = 100
	tickInterval  = time.Second
)

type viewMode int

const (
	viewQueen viewMode = iota
	viewWorker
	viewDAG
)

type inputState int

const (
	inputNone    inputState = iota // objective already provided
	inputWaiting                   // waiting for user to type objective
	inputRunning                   // objective submitted, queen running
)

type keyMap struct {
	Quit       key.Binding
	ScrollUp   key.Binding
	ScrollDown key.Binding

	NextView key.Binding
	PrevView key.Binding

	QueenView   key.Binding
	ToggleDAG   key.Binding
	WorkerLeft  key.Binding
	WorkerRight key.Binding

	TaskFocus  key.Binding
	TaskSelect key.Binding

	ToggleHelp key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.ScrollUp, k.ScrollDown, k.NextView, k.TaskFocus, k.ToggleHelp, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.ScrollUp, k.ScrollDown, k.NextView, k.PrevView},
		{k.WorkerLeft, k.WorkerRight, k.QueenView, k.ToggleDAG},
		{k.TaskFocus, k.TaskSelect, k.ToggleHelp, k.Quit},
	}
}

// TaskInfo tracks task state for display.
type TaskInfo struct {
	ID        string
	Title     string
	Type      string
	Status    string
	WorkerID  string
	DependsOn []string
	Order     int // insertion order
}

// WorkerInfo tracks worker state for display.
type WorkerInfo struct {
	ID     string
	TaskID string
	Status string

	Started time.Time
}

// Model is the Bubble Tea model for the Waggle TUI.
type Model struct {
	// Content
	objective  string
	queenLines []queenLine    // Queen panel lines
	tasks      []TaskInfo     // ordered task list
	taskMap    map[string]int // task ID -> index in tasks slice
	workers    map[string]*WorkerInfo

	// Progress / ETA tracking
	taskStartTimes      map[string]time.Time // task ID -> when it started running
	taskCompletionTimes []time.Duration      // how long each completed task took
	firstTaskStarted    time.Time            // when the very first task began

	// State
	turn      int
	maxTurn   int
	startTime time.Time
	done      bool
	success   bool
	finalMsg  string

	// UI state
	width          int
	height         int
	queenViewport  viewport.Model
	dagViewport    viewport.Model
	viewMode       viewMode
	viewWorkerID   string              // which worker we're viewing
	workerOutputs  map[string][]string // worker ID -> output lines
	workerOrder    []string            // stable insertion-order list of worker IDs
	workerViewport viewport.Model
	taskTable      table.Model
	workerTasks    map[string]string // worker ID -> task title

	// Input mode (interactive start)
	input          inputState
	objectiveInput textinput.Model
	objectiveCh    chan<- string // channel to send objective when submitted

	keys     keyMap
	help     help.Model
	progress progress.Model
	spinner  spinner.Model

	// For tick
	quitting bool
}

type queenLine struct {
	text  string
	style string // "think", "tool", "result", "error", "info"
}

// New creates a new TUI model with a pre-set objective.
func New(objective string, maxTurns int) Model {
	m := Model{
		objective:      objective,
		queenLines:     []queenLine{},
		tasks:          []TaskInfo{},
		taskMap:        make(map[string]int),
		workers:        make(map[string]*WorkerInfo),
		taskStartTimes: make(map[string]time.Time),
		maxTurn:        maxTurns,
		startTime:      time.Now(),
		workerOutputs:  make(map[string][]string),
		workerTasks:    make(map[string]string),
		input:          inputNone,
	}
	m.initBubbles()
	m.queenViewport = viewport.New(1, 1)
	m.dagViewport = viewport.New(1, 1)
	m.workerViewport = viewport.New(1, 1)
	m.refreshViewports(true, true)
	return m
}

// NewInteractive creates a TUI model that prompts for an objective.
func NewInteractive(maxTurns int, objectiveCh chan<- string) Model {
	m := Model{
		queenLines:     []queenLine{},
		tasks:          []TaskInfo{},
		taskMap:        make(map[string]int),
		workers:        make(map[string]*WorkerInfo),
		taskStartTimes: make(map[string]time.Time),
		maxTurn:        maxTurns,
		startTime:      time.Now(),
		workerOutputs:  make(map[string][]string),
		workerTasks:    make(map[string]string),
		input:          inputWaiting,
		objectiveCh:    objectiveCh,
	}
	m.initBubbles()
	m.objectiveInput.Focus()
	m.queenViewport = viewport.New(1, 1)
	m.dagViewport = viewport.New(1, 1)
	m.workerViewport = viewport.New(1, 1)
	m.refreshViewports(true, true)
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(tickCmd(), tea.WindowSize(), func() tea.Msg { return m.spinner.Tick() })
}

func tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg {
		return TickMsg{}
	})
}

func (m *Model) initBubbles() {
	m.keys = keyMap{
		Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		ScrollUp:   key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("up/k", "scroll up")),
		ScrollDown: key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("down/j", "scroll down")),

		NextView: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next")),
		PrevView: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev")),

		QueenView:   key.NewBinding(key.WithKeys("0"), key.WithHelp("0", "queen")),
		ToggleDAG:   key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "dag")),
		WorkerLeft:  key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("left/h", "worker prev")),
		WorkerRight: key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("right/l", "worker next")),
		TaskFocus:   key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "tasks focus")),
		TaskSelect:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open worker")),

		ToggleHelp: key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	}

	m.help = help.New()
	m.help.ShowAll = false

	m.progress = progress.New(
		progress.WithoutPercentage(),
		progress.WithFillCharacters('█', '░'),
		progress.WithSolidFill("#F5A623"),
		progress.WithWidth(12),
	)

	m.spinner = spinner.New(spinner.WithSpinner(spinner.Line))
	m.spinner.Style = subtleStyle

	m.objectiveInput = textinput.New()
	m.objectiveInput.Prompt = ""
	m.objectiveInput.Placeholder = "What should Waggle do?"
	m.objectiveInput.CharLimit = 4000
	m.objectiveInput.Cursor.Style = subtleStyle.Copy().Reverse(true)
	m.objectiveInput.Width = 60

	m.taskTable = table.New(
		table.WithColumns([]table.Column{
			{Title: "S", Width: 3},
			{Title: "Task", Width: 20},
			{Title: "Worker", Width: 12},
		}),
		table.WithRows([]table.Row{}),
		table.WithWidth(20),
		table.WithHeight(4),
	)
	m.taskTable.Blur()
	taskTableStyles := table.DefaultStyles()
	taskTableStyles.Header = subtleStyle.Copy().Bold(true).Padding(0, 0)
	taskTableStyles.Cell = lipgloss.NewStyle().Padding(0, 0)
	taskTableStyles.Selected = lipgloss.NewStyle().Foreground(colorBlue).Bold(true)
	m.taskTable.SetStyles(taskTableStyles)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		// Input mode: capture text for objective
		if m.input == inputWaiting {
			return m.handleInputKey(msg)
		}

		if m.taskTable.Focused() {
			switch {
			case key.Matches(msg, m.keys.TaskFocus) || msg.String() == "esc":
				m.taskTable.Blur()
				return m, nil
			case key.Matches(msg, m.keys.TaskSelect):
				m.openSelectedTaskWorker()
				return m, nil
			}

			var cmd tea.Cmd
			m.taskTable, cmd = m.taskTable.Update(msg)
			return m, cmd
		}

		switch {
		case key.Matches(msg, m.keys.Quit):
			m.quitting = true
			return m, tea.Quit
		case key.Matches(msg, m.keys.ScrollUp):
			if m.viewMode == viewWorker {
				m.workerViewport.ScrollUp(1)
			} else if m.viewMode == viewDAG {
				m.dagViewport.ScrollUp(1)
			} else {
				m.queenViewport.ScrollUp(1)
			}
		case key.Matches(msg, m.keys.ScrollDown):
			if m.viewMode == viewWorker {
				m.workerViewport.ScrollDown(1)
			} else if m.viewMode == viewDAG {
				m.dagViewport.ScrollDown(1)
			} else {
				m.queenViewport.ScrollDown(1)
			}
		case key.Matches(msg, m.keys.NextView):
			m.cycleView(1)
			m.syncTaskTable()
		case key.Matches(msg, m.keys.PrevView):
			m.cycleView(-1)
			m.syncTaskTable()
		case key.Matches(msg, m.keys.QueenView):
			m.viewMode = viewQueen
			m.queenViewport.GotoBottom()
			m.syncTaskTable()
		case key.Matches(msg, m.keys.ToggleDAG):
			if m.viewMode == viewDAG {
				m.viewMode = viewQueen
				m.queenViewport.GotoBottom()
			} else {
				m.viewMode = viewDAG
				m.syncDAGViewport(true)
			}
		case key.Matches(msg, m.keys.WorkerRight):
			if m.viewMode == viewWorker {
				m.cycleView(1)
			} else if len(m.workerOrder) > 0 {
				m.viewMode = viewWorker
				m.viewWorkerID = m.workerOrder[0]
				m.syncWorkerViewport(true)
			}
			m.syncTaskTable()
		case key.Matches(msg, m.keys.WorkerLeft):
			if m.viewMode == viewWorker {
				m.cycleView(-1)
				m.syncTaskTable()
			}
		case key.Matches(msg, m.keys.TaskFocus):
			m.taskTable.Focus()
		case key.Matches(msg, m.keys.ToggleHelp):
			m.help.ShowAll = !m.help.ShowAll
		default:
			// Any other key quits after done
			if m.done {
				m.quitting = true
				return m, tea.Quit
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.syncInputWidth()
		m.refreshViewports(false, false)

	case TickMsg:
		return m, tickCmd()

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case QueenThinkingMsg:
		m.addQueenLine(msg.Text, "think")
		m.syncQueenViewport(true)

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
		m.syncQueenViewport(true)

	case ToolResultMsg:
		style := "result"
		if msg.IsError {
			style = "error"
		}
		// Split multiline results into separate lines
		result := strings.TrimSpace(msg.Result)
		lines := strings.Split(result, "\n")
		if len(lines) > 8 {
			// Summarize very long results
			for _, l := range lines[:6] {
				m.addQueenLine("  "+strings.TrimSpace(l), style)
			}
			m.addQueenLine(fmt.Sprintf("  ... (%d more lines)", len(lines)-6), "info")
		} else {
			for _, l := range lines {
				l = strings.TrimSpace(l)
				if l != "" {
					m.addQueenLine("  "+l, style)
				}
			}
		}
		m.syncQueenViewport(true)

	case TaskUpdateMsg:
		m.updateTask(msg)
		m.refreshViewports(false, false)

	case WorkerUpdateMsg:
		m.updateWorker(msg)

	case TurnMsg:
		m.turn = msg.Turn
		m.maxTurn = msg.MaxTurn
		m.addQueenLine("", "info") // blank separator
		m.syncQueenViewport(true)

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
		m.addQueenLine("", "info")
		m.addQueenLine("Press any key to exit...", "info")
		m.syncQueenViewport(true)
		// Keep ticking so the view stays rendered
		return m, tickCmd()

	case WorkerOutputMsg:
		lines := strings.Split(msg.Output, "\n")
		m.workerOutputs[msg.WorkerID] = lines
		// Track insertion order
		found := false
		for _, id := range m.workerOrder {
			if id == msg.WorkerID {
				found = true
				break
			}
		}
		if !found {
			m.workerOrder = append(m.workerOrder, msg.WorkerID)
		}
		// Auto-scroll if viewing this worker
		if m.viewMode == viewWorker && m.viewWorkerID == msg.WorkerID {
			m.syncWorkerViewport(true)
		}

	case LogMsg:
		m.addQueenLine(msg.Text, "info")
		m.syncQueenViewport(true)
	}

	return m, nil
}

func (m Model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyCtrlC:
		m.quitting = true
		return m, tea.Quit
	case msg.Type == tea.KeyEnter:
		text := strings.TrimSpace(m.objectiveInput.Value())
		if text == "" {
			return m, nil
		}
		m.objective = text
		m.input = inputRunning
		m.startTime = time.Now()
		m.objectiveInput.Blur()
		// Notify the command layer
		if m.objectiveCh != nil {
			m.objectiveCh <- text
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.objectiveInput, cmd = m.objectiveInput.Update(msg)
	return m, cmd
}

func (m Model) normalizedSize() (w, h int) {
	w = m.width
	if w < 40 {
		w = 80
	}
	h = m.height
	if h < 10 {
		h = 24
	}
	return w, h
}

func (m *Model) syncInputWidth() {
	w, _ := m.normalizedSize()
	inputW := w - 16
	if inputW < 30 {
		inputW = 30
	}
	if inputW > 100 {
		inputW = 100
	}
	// Border + padding in the view consume 2 columns.
	m.objectiveInput.Width = inputW - 2
	if m.objectiveInput.Width < 1 {
		m.objectiveInput.Width = 1
	}
}

func (m Model) taskPanelHeight(totalHeight int) int {
	taskRows := len(m.tasks)
	if taskRows == 0 {
		taskRows = 1
	}
	taskH := taskRows + 3
	if taskH > totalHeight/3 {
		taskH = totalHeight / 3
	}
	return taskH
}

func (m Model) mainPanelSize() (panelWidth, panelHeight int) {
	w, h := m.normalizedSize()
	taskH := m.taskPanelHeight(h)
	statusH := 1

	panelHeight = h - taskH - statusH - 4 // status bar + borders
	if panelHeight < 5 {
		panelHeight = 5
	}
	panelWidth = w - 2 // border eats 2 chars
	if panelWidth < 1 {
		panelWidth = 1
	}
	return panelWidth, panelHeight
}

func (m Model) taskPanelSize() (panelWidth, panelHeight int) {
	w, h := m.normalizedSize()
	panelWidth = w - 2 // border eats 2 chars
	if panelWidth < 1 {
		panelWidth = 1
	}
	panelHeight = m.taskPanelHeight(h)
	if panelHeight < 3 {
		panelHeight = 3
	}
	return panelWidth, panelHeight
}

func (m Model) viewportSize() (contentWidth, contentHeight int) {
	panelW, panelH := m.mainPanelSize()
	contentWidth = panelW - 2
	if contentWidth < 1 {
		contentWidth = 1
	}
	contentHeight = panelH - 1 // title takes one line
	if contentHeight < 1 {
		contentHeight = 1
	}
	return contentWidth, contentHeight
}

func (m *Model) syncQueenViewport(gotoBottom bool) {
	contentW, contentH := m.viewportSize()
	m.queenViewport.Width = contentW
	m.queenViewport.Height = contentH
	m.queenViewport.SetContent(m.renderQueenViewportContent(contentW))
	if gotoBottom {
		m.queenViewport.GotoBottom()
	}
}

func (m *Model) syncWorkerViewport(gotoBottom bool) {
	contentW, contentH := m.viewportSize()
	m.workerViewport.Width = contentW
	m.workerViewport.Height = contentH
	m.workerViewport.SetContent(m.renderWorkerViewportContent(contentW, m.viewWorkerID))
	if gotoBottom {
		m.workerViewport.GotoBottom()
	}
}

func (m *Model) syncDAGViewport(gotoTop bool) {
	contentW, contentH := m.viewportSize()
	m.dagViewport.Width = contentW
	m.dagViewport.Height = contentH
	m.dagViewport.SetContent(m.renderDAGViewportContent(contentW))
	if gotoTop {
		m.dagViewport.GotoTop()
	}
}

func (m *Model) syncTaskTable() {
	panelW, panelH := m.taskPanelSize()

	tableW := panelW - 2
	if tableW < 20 {
		tableW = 20
	}

	contentH := panelH - 1
	if contentH < 3 {
		contentH = 3
	}

	statusW := 3
	workerW := 12
	titleW := tableW - statusW - workerW - 2
	if titleW < 12 {
		titleW = 12
	}

	m.taskTable.SetColumns([]table.Column{
		{Title: "S", Width: statusW},
		{Title: "Task", Width: titleW},
		{Title: "Worker", Width: workerW},
	})

	selectedRow := m.taskTable.Cursor()
	if selectedRow < 0 {
		selectedRow = 0
	}

	rows := make([]table.Row, 0, len(m.tasks))
	for idx, t := range m.tasks {
		taskTitle := t.Title
		if taskTitle == "" {
			taskTitle = t.ID
		}

		worker := ""
		if t.WorkerID != "" {
			wid := t.WorkerID
			if len(wid) > 12 {
				wid = "w-" + wid[len(wid)-6:]
			}
			worker = wid
		}

		if !m.taskTable.Focused() && m.viewMode == viewWorker && t.WorkerID == m.viewWorkerID {
			selectedRow = idx
		}

		rows = append(rows, table.Row{
			statusIcon(t.Status),
			taskTitle,
			worker,
		})
	}

	m.taskTable.SetRows(rows)
	m.taskTable.SetWidth(tableW)
	m.taskTable.SetHeight(contentH)
	if len(rows) == 0 {
		m.taskTable.SetCursor(0)
		return
	}
	if selectedRow >= len(rows) {
		selectedRow = len(rows) - 1
	}
	m.taskTable.SetCursor(selectedRow)
}

func (m *Model) openSelectedTaskWorker() {
	idx := m.taskTable.Cursor()
	if idx < 0 || idx >= len(m.tasks) {
		return
	}

	workerID := m.tasks[idx].WorkerID
	if workerID == "" {
		return
	}

	m.taskTable.Blur()
	m.viewMode = viewWorker
	m.viewWorkerID = workerID
	m.syncWorkerViewport(true)
	m.syncTaskTable()
}

func (m *Model) refreshViewports(gotoQueenBottom, gotoWorkerBottom bool) {
	m.syncQueenViewport(gotoQueenBottom)
	m.syncWorkerViewport(gotoWorkerBottom)
	m.syncDAGViewport(false)
	m.syncTaskTable()
}

func (m *Model) cycleView(direction int) {
	if len(m.workerOrder) == 0 {
		m.viewMode = viewQueen
		return
	}

	if m.viewMode == viewQueen {
		// Enter worker view
		m.viewMode = viewWorker
		if direction > 0 {
			m.viewWorkerID = m.workerOrder[0]
		} else {
			m.viewWorkerID = m.workerOrder[len(m.workerOrder)-1]
		}
		m.syncWorkerViewport(true)
		return
	}

	// Find current index
	idx := -1
	for i, id := range m.workerOrder {
		if id == m.viewWorkerID {
			idx = i
			break
		}
	}

	next := idx + direction
	if next < 0 || next >= len(m.workerOrder) {
		// Wrap back to queen
		m.viewMode = viewQueen
		m.queenViewport.GotoBottom()
		return
	}

	m.viewWorkerID = m.workerOrder[next]
	m.syncWorkerViewport(true)
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
		if len(msg.DependsOn) > 0 {
			m.tasks[idx].DependsOn = msg.DependsOn
		}
	} else {
		m.taskMap[msg.ID] = len(m.tasks)
		m.tasks = append(m.tasks, TaskInfo{
			ID:        msg.ID,
			Title:     msg.Title,
			Type:      msg.Type,
			Status:    msg.Status,
			WorkerID:  msg.WorkerID,
			DependsOn: msg.DependsOn,
			Order:     len(m.tasks),
		})
	}

	// Track task timing for ETA calculation
	now := time.Now()
	switch msg.Status {
	case "running":
		if m.firstTaskStarted.IsZero() {
			m.firstTaskStarted = now
		}
		if _, started := m.taskStartTimes[msg.ID]; !started {
			m.taskStartTimes[msg.ID] = now
		}
	case "complete":
		if started, ok := m.taskStartTimes[msg.ID]; ok {
			m.taskCompletionTimes = append(m.taskCompletionTimes, now.Sub(started))
			delete(m.taskStartTimes, msg.ID)
		}
	case "failed":
		delete(m.taskStartTimes, msg.ID)
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
		Started: time.Now(),
	}
	// Track worker→task mapping for display
	if msg.TaskID != "" {
		// Look up task title
		if idx, ok := m.taskMap[msg.TaskID]; ok && idx < len(m.tasks) {
			m.workerTasks[msg.ID] = m.tasks[idx].Title
		} else {
			m.workerTasks[msg.ID] = msg.TaskID
		}
	}
}

// taskStats returns (completed, running, failed, total) counts.
func (m Model) taskStats() (done, running, failed, total int) {
	total = len(m.tasks)
	for _, t := range m.tasks {
		switch t.Status {
		case "complete":
			done++
		case "running":
			running++
		case "failed":
			failed++
		}
	}
	return
}

// estimateETA returns the estimated time remaining, or zero if unavailable.
func (m Model) estimateETA() time.Duration {
	if len(m.taskCompletionTimes) == 0 {
		return 0
	}
	done, _, _, total := m.taskStats()
	remaining := total - done
	if remaining <= 0 {
		return 0
	}
	var sum time.Duration
	for _, d := range m.taskCompletionTimes {
		sum += d
	}
	avg := sum / time.Duration(len(m.taskCompletionTimes))
	return avg * time.Duration(remaining)
}

// formatETA formats a duration into a concise human-readable string.
func formatETA(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("~%ds remaining", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		if s > 0 {
			return fmt.Sprintf("~%dm%ds remaining", m, s)
		}
		return fmt.Sprintf("~%dm remaining", m)
	}
	h := int(d.Hours())
	min := int(d.Minutes()) % 60
	return fmt.Sprintf("~%dh%dm remaining", h, min)
}
