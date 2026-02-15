package output

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/exedev/waggle/internal/task"
)

// EventType represents the type of JSON output event.
type EventType string

const (
	// EventSessionStart marks the beginning of a session.
	EventSessionStart EventType = "session_start"
	// EventSessionEnd marks the end of a session with final results.
	EventSessionEnd EventType = "session_end"
	// EventTaskCreated is emitted when a task is created.
	EventTaskCreated EventType = "task_created"
	// EventTaskUpdated is emitted when a task status changes.
	EventTaskUpdated EventType = "task_updated"
	// EventTaskCompleted is emitted when a task finishes (success or failure).
	EventTaskCompleted EventType = "task_completed"
	// EventWorkerSpawned is emitted when a worker starts.
	EventWorkerSpawned EventType = "worker_spawned"
	// EventWorkerOutput is emitted when a worker produces output.
	EventWorkerOutput EventType = "worker_output"
	// EventQueenDecision is emitted when the queen makes a decision.
	EventQueenDecision EventType = "queen_decision"
	// EventError is emitted when an error occurs.
	EventError EventType = "error"
)

// TaskEvent represents a task-related event.
type TaskEvent struct {
	TaskID      string        `json:"task_id"`
	Title       string        `json:"title,omitempty"`
	Type        string        `json:"type,omitempty"`
	Status      string        `json:"status"`
	WorkerID    string        `json:"worker_id,omitempty"`
	ParentID    string        `json:"parent_id,omitempty"`
	Priority    int           `json:"priority,omitempty"`
	Description string        `json:"description,omitempty"`
	DependsOn   []string      `json:"depends_on,omitempty"`
	MaxRetries  int           `json:"max_retries,omitempty"`
	RetryCount  int           `json:"retry_count,omitempty"`
	LastError   string        `json:"last_error,omitempty"`
	CreatedAt   *time.Time    `json:"created_at,omitempty"`
	StartedAt   *time.Time    `json:"started_at,omitempty"`
	CompletedAt *time.Time    `json:"completed_at,omitempty"`
	Result      *TaskResult   `json:"result,omitempty"`
}

// TaskResult represents the result of a completed task.
type TaskResult struct {
	Success   bool              `json:"success"`
	Output    string            `json:"output,omitempty"`
	Errors    []string          `json:"errors,omitempty"`
	Artifacts map[string]string `json:"artifacts,omitempty"`
	Metrics   map[string]float64 `json:"metrics,omitempty"`
}

// SessionSummary represents the final session summary.
type SessionSummary struct {
	SessionID      string            `json:"session_id"`
	Objective      string            `json:"objective"`
	Status         string            `json:"status"`
	TotalTasks     int               `json:"total_tasks"`
	CompletedTasks int               `json:"completed_tasks"`
	FailedTasks    int               `json:"failed_tasks"`
	Iterations     int               `json:"iterations"`
	Duration       time.Duration     `json:"duration_ms"`
	Tasks          []TaskEvent       `json:"tasks,omitempty"`
}

// JSONEvent is the wrapper for all JSON output events.
type JSONEvent struct {
	Type      EventType     `json:"type"`
	Timestamp time.Time     `json:"timestamp"`
	SessionID string        `json:"session_id,omitempty"`
	Task      *TaskEvent    `json:"task,omitempty"`
	Worker    *WorkerEvent  `json:"worker,omitempty"`
	Session   *SessionSummary `json:"session,omitempty"`
	Error     *ErrorEvent   `json:"error,omitempty"`
	Message   string        `json:"message,omitempty"`
	Data      interface{}   `json:"data,omitempty"`
}

// WorkerEvent represents a worker-related event.
type WorkerEvent struct {
	WorkerID string `json:"worker_id"`
	TaskID   string `json:"task_id,omitempty"`
	Status   string `json:"status,omitempty"`
	Output   string `json:"output,omitempty"`
}

// ErrorEvent represents an error that occurred.
type ErrorEvent struct {
	Message   string `json:"message"`
	TaskID    string `json:"task_id,omitempty"`
	WorkerID  string `json:"worker_id,omitempty"`
	ErrorType string `json:"error_type,omitempty"`
}

// JSONWriter handles JSON output serialization.
type JSONWriter struct {
	mu         sync.Mutex
	w          io.Writer
	sessionID  string
	startTime  time.Time
	taskEvents []TaskEvent
	maxOutput  int // Maximum output length before truncation
}

// NewJSONWriter creates a new JSON writer.
func NewJSONWriter(w io.Writer, sessionID string) *JSONWriter {
	return &JSONWriter{
		w:         w,
		sessionID: sessionID,
		startTime: time.Now(),
		maxOutput: 10000, // Truncate very large outputs at 10KB
	}
}

// SetMaxOutput sets the maximum output size before truncation.
func (jw *JSONWriter) SetMaxOutput(max int) {
	jw.maxOutput = max
}

// writeEvent writes a single JSON event as a line.
func (jw *JSONWriter) writeEvent(event JSONEvent) error {
	jw.mu.Lock()
	defer jw.mu.Unlock()

	event.Timestamp = time.Now()
	if jw.sessionID != "" {
		event.SessionID = jw.sessionID
	}

	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(jw.w, string(data))
	return err
}

// WriteSessionStart emits a session start event.
func (jw *JSONWriter) WriteSessionStart(objective string, data map[string]interface{}) error {
	event := JSONEvent{
		Type:    EventSessionStart,
		Message: objective,
		Data:    data,
	}
	return jw.writeEvent(event)
}

// WriteSessionEnd emits the final session summary.
func (jw *JSONWriter) WriteSessionEnd(summary SessionSummary) error {
	summary.SessionID = jw.sessionID
	summary.Duration = time.Since(jw.startTime)

	event := JSONEvent{
		Type:    EventSessionEnd,
		Session: &summary,
	}
	return jw.writeEvent(event)
}

// WriteTaskCreated emits a task created event.
func (jw *JSONWriter) WriteTaskCreated(t *task.Task) error {
	taskEvent := taskToEvent(t, jw.maxOutput)
	
	jw.mu.Lock()
	jw.taskEvents = append(jw.taskEvents, *taskEvent)
	jw.mu.Unlock()

	event := JSONEvent{
		Type: EventTaskCreated,
		Task: taskEvent,
	}
	return jw.writeEvent(event)
}

// WriteTaskUpdated emits a task status update event.
func (jw *JSONWriter) WriteTaskUpdated(taskID, status, workerID string) error {
	event := JSONEvent{
		Type: EventTaskUpdated,
		Task: &TaskEvent{
			TaskID:   taskID,
			Status:   status,
			WorkerID: workerID,
		},
	}
	return jw.writeEvent(event)
}

// WriteTaskCompleted emits a task completion event.
func (jw *JSONWriter) WriteTaskCompleted(t *task.Task) error {
	taskEvent := taskToEvent(t, jw.maxOutput)
	
	// Update stored task event
	jw.mu.Lock()
	for i, te := range jw.taskEvents {
		if te.TaskID == t.ID {
			jw.taskEvents[i] = *taskEvent
			break
		}
	}
	jw.mu.Unlock()

	event := JSONEvent{
		Type: EventTaskCompleted,
		Task: taskEvent,
	}
	return jw.writeEvent(event)
}

// WriteWorkerSpawned emits a worker spawned event.
func (jw *JSONWriter) WriteWorkerSpawned(workerID, taskID string) error {
	event := JSONEvent{
		Type: EventWorkerSpawned,
		Worker: &WorkerEvent{
			WorkerID: workerID,
			TaskID:   taskID,
			Status:   "running",
		},
	}
	return jw.writeEvent(event)
}

// WriteWorkerOutput emits a worker output event.
func (jw *JSONWriter) WriteWorkerOutput(workerID, output string) error {
	if len(output) > jw.maxOutput {
		output = output[:jw.maxOutput] + "... [truncated]"
	}

	event := JSONEvent{
		Type: EventWorkerOutput,
		Worker: &WorkerEvent{
			WorkerID: workerID,
			Output:   output,
		},
	}
	return jw.writeEvent(event)
}

// WriteError emits an error event.
func (jw *JSONWriter) WriteError(message, taskID, workerID, errorType string) error {
	event := JSONEvent{
		Type: EventError,
		Error: &ErrorEvent{
			Message:   message,
			TaskID:    taskID,
			WorkerID:  workerID,
			ErrorType: errorType,
		},
	}
	return jw.writeEvent(event)
}

// WriteQueenDecision emits a queen decision event.
func (jw *JSONWriter) WriteQueenDecision(phase, message string, data map[string]interface{}) error {
	event := JSONEvent{
		Type:    EventQueenDecision,
		Message: message,
		Data: map[string]interface{}{
			"phase": phase,
			"data":  data,
		},
	}
	return jw.writeEvent(event)
}

// GetTaskEvents returns all tracked task events.
func (jw *JSONWriter) GetTaskEvents() []TaskEvent {
	jw.mu.Lock()
	defer jw.mu.Unlock()
	
	result := make([]TaskEvent, len(jw.taskEvents))
	copy(result, jw.taskEvents)
	return result
}

// SetSessionID sets the session ID (used when session is created after writer).
func (jw *JSONWriter) SetSessionID(sessionID string) {
	jw.mu.Lock()
	defer jw.mu.Unlock()
	jw.sessionID = sessionID
}

// taskToEvent converts a task.Task to a TaskEvent.
func taskToEvent(t *task.Task, maxOutput int) *TaskEvent {
	e := &TaskEvent{
		TaskID:      t.ID,
		Title:       t.Title,
		Type:        string(t.Type),
		Status:      string(t.Status),
		WorkerID:    t.WorkerID,
		ParentID:    t.ParentID,
		Priority:    int(t.Priority),
		Description: t.Description,
		DependsOn:   t.DependsOn,
		MaxRetries:  t.MaxRetries,
		RetryCount:  t.RetryCount,
		LastError:   t.LastError,
	}

	if !t.CreatedAt.IsZero() {
		e.CreatedAt = &t.CreatedAt
	}
	if t.StartedAt != nil {
		e.StartedAt = t.StartedAt
	}
	if t.CompletedAt != nil {
		e.CompletedAt = t.CompletedAt
	}

	if t.Result != nil {
		output := t.Result.Output
		if len(output) > maxOutput {
			output = output[:maxOutput] + "... [truncated]"
		}

		e.Result = &TaskResult{
			Success:   t.Result.Success,
			Output:    output,
			Errors:    t.Result.Errors,
			Artifacts: t.Result.Artifacts,
			Metrics:   t.Result.Metrics,
		}
	}

	return e
}

// TaskEventFromTask creates a TaskEvent from a task.Task (public helper).
func TaskEventFromTask(t *task.Task) *TaskEvent {
	return taskToEvent(t, 10000)
}
