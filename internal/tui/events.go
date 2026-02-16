package tui

// TUI event types — sent from the Queen/workers to the TUI via tea.Program.Send()

// QueenThinkingMsg is the Queen's text output (her reasoning).
type QueenThinkingMsg struct {
	Text string
}

// ToolCallMsg is when the Queen invokes a tool.
type ToolCallMsg struct {
	Name  string
	Input string // truncated JSON
}

// ToolResultMsg is the result of a tool call.
type ToolResultMsg struct {
	Name    string
	Result  string // truncated
	IsError bool
}

// TaskUpdateMsg is a task status change.
type TaskUpdateMsg struct {
	ID        string
	Title     string
	Type      string
	Status    string
	WorkerID  string
	DependsOn []string
}

// WorkerUpdateMsg is a worker status change.
type WorkerUpdateMsg struct {
	ID     string
	TaskID string
	Status string // "running", "idle", "done", "failed"
}

// TurnMsg indicates a new agent turn.
type TurnMsg struct {
	Turn    int
	MaxTurn int
}

// DoneMsg indicates the run is complete.
type DoneMsg struct {
	Success bool
	Summary string
	Error   string
}

// TickMsg is a periodic timer for updating elapsed times.
type TickMsg struct{}

// LogMsg is a raw log line (fallback for non-structured output).
type LogMsg struct {
	Text string
}

// WorkerOutputMsg carries accumulated output for a worker.
type WorkerOutputMsg struct {
	WorkerID string
	Output   string
}
