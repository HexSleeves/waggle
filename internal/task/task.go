package task

import (
	"fmt"
	"sync"
	"time"

	"github.com/exedev/waggle/internal/bus"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusAssigned  Status = "assigned"
	StatusRunning   Status = "running"
	StatusComplete  Status = "complete"
	StatusFailed    Status = "failed"
	StatusRetrying  Status = "retrying"
	StatusCancelled Status = "cancelled"
)

type Priority int

const (
	PriorityLow      Priority = 0
	PriorityNormal   Priority = 1
	PriorityHigh     Priority = 2
	PriorityCritical Priority = 3
)

type Type string

const (
	TypeCode     Type = "code"
	TypeResearch Type = "research"
	TypeTest     Type = "test"
	TypeReview   Type = "review"
	TypeGeneric  Type = "generic"
)

type Task struct {
	mu sync.RWMutex `json:"-"`

	ID            string            `json:"id"`
	ParentID      string            `json:"parent_id,omitempty"`
	Type          Type              `json:"type"`
	Status        Status            `json:"status"`
	Priority      Priority          `json:"priority"`
	Title         string            `json:"title"`
	Description   string            `json:"description"`
	Constraints   []string          `json:"constraints,omitempty"`
	Context       map[string]string `json:"context,omitempty"`
	AllowedPaths  []string          `json:"allowed_paths,omitempty"`
	WorkerID      string            `json:"worker_id,omitempty"`
	Result        *Result           `json:"result,omitempty"`
	MaxRetries    int               `json:"max_retries"`
	RetryCount    int               `json:"retry_count"`
	LastError     string            `json:"last_error,omitempty"`
	LastErrorType string            `json:"last_error_type,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	StartedAt     *time.Time        `json:"started_at,omitempty"`
	CompletedAt   *time.Time        `json:"completed_at,omitempty"`
	Timeout       time.Duration     `json:"timeout,omitempty"`
	RetryAfter    time.Time         `json:"retry_after,omitempty"` // backoff: don't schedule before this time
	DependsOn     []string          `json:"depends_on,omitempty"`
}

// SetResult sets the task result (thread-safe).
func (t *Task) SetResult(r *Result) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Result = r
}

// GetResult returns the task result (thread-safe).
func (t *Task) GetResult() *Result {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Result
}

// SetWorkerID sets the assigned worker ID (thread-safe).
func (t *Task) SetWorkerID(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.WorkerID = id
}

// GetWorkerID returns the assigned worker ID (thread-safe).
func (t *Task) GetWorkerID() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.WorkerID
}

// SetDescription updates the task description (thread-safe).
func (t *Task) SetDescription(desc string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Description = desc
}

// AppendDescription appends text to the description (thread-safe).
func (t *Task) AppendDescription(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Description += text
}

// GetDescription returns the task description (thread-safe).
func (t *Task) GetDescription() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Description
}

// IncrRetryCount increments and returns the new retry count (thread-safe).
func (t *Task) IncrRetryCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.RetryCount++
	return t.RetryCount
}

// GetRetryCount returns the current retry count (thread-safe).
func (t *Task) GetRetryCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.RetryCount
}

// SetLastError sets the last error info (thread-safe).
func (t *Task) SetLastError(msg, errType string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.LastError = msg
	t.LastErrorType = errType
}

// GetLastError returns the last error message and type (thread-safe).
func (t *Task) GetLastError() (string, string) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.LastError, t.LastErrorType
}

// SetRetryAfter sets the backoff time (thread-safe).
func (t *Task) SetRetryAfter(after time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.RetryAfter = after
}

// SetConstraints replaces the constraints list (thread-safe).
func (t *Task) SetConstraints(c []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Constraints = c
}

// GetConstraints returns a copy of the constraints (thread-safe).
func (t *Task) GetConstraints() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	cp := make([]string, len(t.Constraints))
	copy(cp, t.Constraints)
	return cp
}

// GetStatus returns the current status (thread-safe).
func (t *Task) GetStatus() Status {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Status
}

type Result struct {
	Success   bool               `json:"success"`
	Output    string             `json:"output"`
	Errors    []string           `json:"errors,omitempty"`
	Artifacts map[string]string  `json:"artifacts,omitempty"`
	Metrics   map[string]float64 `json:"metrics,omitempty"`
}

// TaskGraph manages tasks and their dependencies
type TaskGraph struct {
	mu    sync.RWMutex
	tasks map[string]*Task
	bus   *bus.MessageBus
}

func NewTaskGraph(b *bus.MessageBus) *TaskGraph {
	return &TaskGraph{
		tasks: make(map[string]*Task),
		bus:   b,
	}
}

func (g *TaskGraph) Add(t *Task) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tasks[t.ID] = t
	if g.bus != nil {
		g.bus.Publish(bus.Message{
			Type:    bus.MsgTaskCreated,
			TaskID:  t.ID,
			Payload: t,
			Time:    time.Now(),
		})
	}
}

// Remove deletes a task from the graph by ID.
func (g *TaskGraph) Remove(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.tasks, id)
}

func (g *TaskGraph) Get(id string) (*Task, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	t, ok := g.tasks[id]
	return t, ok
}

func (g *TaskGraph) UpdateStatus(id string, status Status) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	t, ok := g.tasks[id]
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	old := t.Status
	t.Status = status
	now := time.Now()
	switch status {
	case StatusRunning:
		t.StartedAt = &now
	case StatusComplete, StatusFailed, StatusCancelled:
		t.CompletedAt = &now
	}
	if g.bus != nil {
		g.bus.Publish(bus.Message{
			Type:    bus.MsgTaskStatusChanged,
			TaskID:  id,
			Payload: map[string]Status{"old": old, "new": status},
			Time:    now,
		})
	}
	return nil
}

func (g *TaskGraph) Ready() []*Task {
	g.mu.RLock()
	defer g.mu.RUnlock()
	now := time.Now()
	var ready []*Task
	for _, t := range g.tasks {
		if t.Status != StatusPending {
			continue
		}
		// Respect backoff: skip tasks whose RetryAfter hasn't elapsed
		if !t.RetryAfter.IsZero() && now.Before(t.RetryAfter) {
			continue
		}
		allDone := true
		for _, depID := range t.DependsOn {
			dep, ok := g.tasks[depID]
			if !ok || dep.Status != StatusComplete {
				allDone = false
				break
			}
		}
		if allDone {
			ready = append(ready, t)
		}
	}
	return ready
}

func (g *TaskGraph) AllComplete() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, t := range g.tasks {
		if t.Status != StatusComplete && t.Status != StatusCancelled {
			return false
		}
	}
	return len(g.tasks) > 0
}

func (g *TaskGraph) All() []*Task {
	g.mu.RLock()
	defer g.mu.RUnlock()
	all := make([]*Task, 0, len(g.tasks))
	for _, t := range g.tasks {
		all = append(all, t)
	}
	return all
}

func (g *TaskGraph) Failed() []*Task {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var failed []*Task
	for _, t := range g.tasks {
		if t.Status == StatusFailed {
			failed = append(failed, t)
		}
	}
	return failed
}

// DetectCycles detects circular dependencies in the task graph using DFS.
// Returns an error describing the cycle if found, or nil if no cycles exist.
func (g *TaskGraph) DetectCycles() error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// Track visited nodes (fully processed)
	visited := make(map[string]bool)
	// Track nodes in current recursion stack (being processed)
	recStack := make(map[string]bool)

	for id := range g.tasks {
		if !visited[id] {
			if cycle := g.detectCycleDFS(id, visited, recStack, []string{}); cycle != nil {
				return fmt.Errorf("circular dependency detected: %s", formatCycle(cycle))
			}
		}
	}
	return nil
}

// detectCycleDFS performs DFS from the given node to detect cycles.
// Returns the cycle path if a cycle is detected, nil otherwise.
func (g *TaskGraph) detectCycleDFS(nodeID string, visited, recStack map[string]bool, parentPath []string) []string {
	visited[nodeID] = true
	recStack[nodeID] = true
	// Create a new slice to avoid aliasing the caller's backing array
	path := make([]string, len(parentPath)+1)
	copy(path, parentPath)
	path[len(parentPath)] = nodeID

	task, ok := g.tasks[nodeID]
	if !ok {
		recStack[nodeID] = false
		return nil
	}

	for _, depID := range task.DependsOn {
		// Skip if dependency doesn't exist in the graph
		if _, exists := g.tasks[depID]; !exists {
			continue
		}

		if recStack[depID] {
			// Cycle found - construct the cycle path
			cycleStart := -1
			for i, id := range path {
				if id == depID {
					cycleStart = i
					break
				}
			}
			if cycleStart >= 0 {
				cycle := make([]string, len(path[cycleStart:])+1)
				copy(cycle, path[cycleStart:])
				cycle[len(cycle)-1] = depID
				return cycle
			}
		}

		if !visited[depID] {
			if cycle := g.detectCycleDFS(depID, visited, recStack, path); cycle != nil {
				return cycle
			}
		}
	}

	recStack[nodeID] = false
	return nil
}

// formatCycle formats a cycle path as a string like "a -> b -> c -> a"
func formatCycle(cycle []string) string {
	if len(cycle) == 0 {
		return ""
	}
	result := cycle[0]
	for i := 1; i < len(cycle); i++ {
		result += " -> " + cycle[i]
	}
	return result
}
