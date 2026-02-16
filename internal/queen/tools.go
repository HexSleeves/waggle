package queen

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/exedev/waggle/internal/blackboard"
	"github.com/exedev/waggle/internal/llm"
	"github.com/exedev/waggle/internal/state"
	"github.com/exedev/waggle/internal/task"
	"github.com/exedev/waggle/internal/worker"
)

// ToolHandler executes a tool call and returns the result string.
type ToolHandler func(ctx context.Context, q *Queen, input json.RawMessage) (string, error)

// toolHandlers maps tool names to their handler functions.
var toolHandlers = map[string]ToolHandler{
	"create_tasks":     handleCreateTasks,
	"assign_task":      handleAssignTask,
	"get_status":       handleGetStatus,
	"get_task_output":  handleGetTaskOutput,
	"approve_task":     handleApproveTask,
	"reject_task":      handleRejectTask,
	"wait_for_workers": handleWaitForWorkers,
	"read_file":        handleReadFile,
	"list_files":       handleListFiles,
	"complete":         handleComplete,
	"fail":             handleFail,
}

// executeTool runs a tool call and returns the result.
func (q *Queen) executeTool(ctx context.Context, tc *llm.ToolCall) (string, error) {
	handler, ok := toolHandlers[tc.Name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", tc.Name)
	}
	return handler(ctx, q, tc.Input)
}

// queenTools returns all tool definitions for the Queen agent.
func queenTools() []llm.ToolDef {
	return []llm.ToolDef{
		{
			Name:        "create_tasks",
			Description: "Create one or more tasks in the task graph. Tasks can have dependencies on each other.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"tasks": map[string]interface{}{
						"type":        "array",
						"description": "List of tasks to create",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"id":            map[string]interface{}{"type": "string", "description": "Unique task identifier"},
								"title":         map[string]interface{}{"type": "string", "description": "Short task title"},
								"description":   map[string]interface{}{"type": "string", "description": "Detailed task description"},
								"type":          map[string]interface{}{"type": "string", "enum": []string{"code", "research", "test", "review", "generic"}},
								"priority":      map[string]interface{}{"type": "integer", "minimum": 0, "maximum": 3},
								"depends_on":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
								"constraints":   map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
								"allowed_paths": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
								"max_retries":   map[string]interface{}{"type": "integer"},
							},
							"required": []string{"id", "title", "description", "type"},
						},
					},
				},
				"required": []string{"tasks"},
			},
		},
		{
			Name:        "assign_task",
			Description: "Assign a pending task to a worker bee. The task must be in pending status with all dependencies met.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"task_id": map[string]interface{}{"type": "string", "description": "ID of the task to assign"},
				},
				"required": []string{"task_id"},
			},
		},
		{
			Name:        "get_status",
			Description: "Get current status of all tasks and workers.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "get_task_output",
			Description: "Get the output or error information from a completed or failed task.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"task_id": map[string]interface{}{"type": "string", "description": "ID of the task"},
				},
				"required": []string{"task_id"},
			},
		},
		{
			Name:        "approve_task",
			Description: "Approve a completed task's output. Optionally provide feedback posted to the blackboard.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"task_id":  map[string]interface{}{"type": "string", "description": "ID of the task to approve"},
					"feedback": map[string]interface{}{"type": "string", "description": "Optional feedback"},
				},
				"required": []string{"task_id"},
			},
		},
		{
			Name:        "reject_task",
			Description: "Reject a task's output and re-queue it for retry with feedback appended.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"task_id":  map[string]interface{}{"type": "string", "description": "ID of the task to reject"},
					"feedback": map[string]interface{}{"type": "string", "description": "Reason for rejection and guidance for retry"},
				},
				"required": []string{"task_id", "feedback"},
			},
		},
		{
			Name:        "wait_for_workers",
			Description: "Block until at least one running worker completes or fails. Returns summary of changes.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"timeout_seconds": map[string]interface{}{"type": "integer", "description": "Max seconds to wait (default 300)"},
				},
			},
		},
		{
			Name:        "read_file",
			Description: "Read a project file (safety-checked). Optionally read only specific lines.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path":       map[string]interface{}{"type": "string", "description": "File path relative to project root"},
					"line_start": map[string]interface{}{"type": "integer", "description": "Starting line number (1-based, optional)"},
					"line_end":   map[string]interface{}{"type": "integer", "description": "Ending line number (inclusive, optional)"},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "list_files",
			Description: "List files in a directory, optionally filtered by glob pattern.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path":    map[string]interface{}{"type": "string", "description": "Directory path relative to project root (default: .)"},
					"pattern": map[string]interface{}{"type": "string", "description": "Glob pattern to filter files (e.g. *.go)"},
				},
			},
		},
		{
			Name:        "complete",
			Description: "Declare the objective done. Provide a summary of what was accomplished.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"summary": map[string]interface{}{"type": "string", "description": "Summary of what was accomplished"},
				},
				"required": []string{"summary"},
			},
		},
		{
			Name:        "fail",
			Description: "Declare the objective failed. Provide a reason.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"reason": map[string]interface{}{"type": "string", "description": "Reason for failure"},
				},
				"required": []string{"reason"},
			},
		},
	}
}

// ---------- create_tasks ----------

type createTasksInput struct {
	Tasks []createTaskEntry `json:"tasks"`
}

type createTaskEntry struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Type         string   `json:"type"`
	Priority     int      `json:"priority"`
	DependsOn    []string `json:"depends_on"`
	Constraints  []string `json:"constraints"`
	AllowedPaths []string `json:"allowed_paths"`
	MaxRetries   int      `json:"max_retries"`
}

func handleCreateTasks(ctx context.Context, q *Queen, input json.RawMessage) (string, error) {
	var in createTasksInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if len(in.Tasks) == 0 {
		return "", fmt.Errorf("tasks array is required and must not be empty")
	}

	// Validate each task entry
	for i, te := range in.Tasks {
		if te.ID == "" {
			return "", fmt.Errorf("task[%d]: id is required", i)
		}
		if te.Title == "" {
			return "", fmt.Errorf("task[%d]: title is required", i)
		}
		if te.Description == "" {
			return "", fmt.Errorf("task[%d]: description is required", i)
		}
		if te.Type == "" {
			return "", fmt.Errorf("task[%d]: type is required", i)
		}
		// Check for duplicate IDs with existing tasks
		if _, exists := q.tasks.Get(te.ID); exists {
			return "", fmt.Errorf("task[%d]: id %q already exists in task graph", i, te.ID)
		}
	}

	// Build task objects
	created := make([]*task.Task, 0, len(in.Tasks))
	for _, te := range in.Tasks {
		maxRetries := te.MaxRetries
		if maxRetries == 0 {
			maxRetries = q.cfg.Workers.MaxRetries
		}
		t := &task.Task{
			ID:           te.ID,
			Type:         task.Type(te.Type),
			Status:       task.StatusPending,
			Priority:     task.Priority(te.Priority),
			Title:        te.Title,
			Description:  te.Description,
			Constraints:  te.Constraints,
			AllowedPaths: te.AllowedPaths,
			DependsOn:    te.DependsOn,
			MaxRetries:   maxRetries,
			CreatedAt:    time.Now(),
			Timeout:      q.cfg.Workers.DefaultTimeout,
		}
		created = append(created, t)
	}

	// Add to graph
	for _, t := range created {
		q.tasks.Add(t)
	}

	// Cycle detection — rollback on failure
	if err := q.tasks.DetectCycles(); err != nil {
		// Remove the tasks we just added
		for _, t := range created {
			q.tasks.Remove(t.ID)
		}
		return "", fmt.Errorf("cycle detected, tasks rolled back: %w", err)
	}

	// Persist to DB (tasks already added to graph above for cycle detection)
	for _, t := range created {
		if err := q.db.InsertTask(ctx, q.sessionID, state.TaskRow{
			ID: t.ID, Type: string(t.Type), Status: string(t.Status),
			Priority: int(t.Priority), Title: t.Title, Description: t.Description,
			MaxRetries: t.MaxRetries, DependsOn: strings.Join(t.DependsOn, ","),
		}); err != nil {
			q.logger.Printf("⚠ Warning: failed to insert task: %v", err)
		}
	}

	// Build summary
	var b strings.Builder
	fmt.Fprintf(&b, "Created %d task(s):\n", len(created))
	for _, t := range created {
		fmt.Fprintf(&b, "  - [%s] %s (id=%s, priority=%d)\n", t.Type, t.Title, t.ID, t.Priority)
	}
	return b.String(), nil
}

// ---------- assign_task ----------

type assignTaskInput struct {
	TaskID string `json:"task_id"`
}

func handleAssignTask(ctx context.Context, q *Queen, input json.RawMessage) (string, error) {
	var in assignTaskInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if in.TaskID == "" {
		return "", fmt.Errorf("task_id is required")
	}

	t, ok := q.tasks.Get(in.TaskID)
	if !ok {
		return "", fmt.Errorf("task %q not found", in.TaskID)
	}
	if t.Status != task.StatusPending {
		return "", fmt.Errorf("task %q is not pending (current status: %s)", in.TaskID, t.Status)
	}

	// Check dependencies are met
	for _, depID := range t.DependsOn {
		dep, depOK := q.tasks.Get(depID)
		if !depOK || dep.Status != task.StatusComplete {
			return "", fmt.Errorf("task %q has unmet dependency: %s", in.TaskID, depID)
		}
	}

	// Check pool capacity
	if q.pool.ActiveCount() >= q.cfg.Workers.MaxParallel {
		return "", fmt.Errorf("max parallel workers (%d) reached, wait for a worker to finish", q.cfg.Workers.MaxParallel)
	}

	adapterName := q.router.Route(t)
	if adapterName == "" {
		return "", fmt.Errorf("no adapter available for task type %s", t.Type)
	}

	// Inject default scope constraints (shared with delegate())
	injectDefaultConstraints(t)

	bee, err := q.pool.Spawn(ctx, t, adapterName)
	if err != nil {
		return "", fmt.Errorf("spawn worker: %w", err)
	}

	q.mu.Lock()
	q.assignments[bee.ID()] = t.ID
	q.mu.Unlock()

	if err := q.tasks.UpdateStatus(t.ID, task.StatusRunning); err != nil {
		q.logger.Printf("⚠ Warning: failed to update task status: %v", err)
	}
	t.WorkerID = bee.ID()

	if err := q.db.UpdateTaskStatus(ctx, q.sessionID, t.ID, "running"); err != nil {
		q.logger.Printf("⚠ Warning: failed to update task status: %v", err)
	}
	if err := q.db.UpdateTaskWorker(ctx, q.sessionID, t.ID, bee.ID()); err != nil {
		q.logger.Printf("⚠ Warning: failed to update task worker: %v", err)
	}

	return fmt.Sprintf("Task %q assigned to worker %s (adapter: %s)", t.ID, bee.ID(), adapterName), nil
}

// ---------- get_status ----------

func handleGetStatus(ctx context.Context, q *Queen, input json.RawMessage) (string, error) {
	allTasks := q.tasks.All()

	type taskInfo struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Type     string `json:"type"`
		Status   string `json:"status"`
		WorkerID string `json:"worker_id,omitempty"`
		Priority int    `json:"priority"`
	}

	infos := make([]taskInfo, 0, len(allTasks))
	counts := map[string]int{}
	for _, t := range allTasks {
		infos = append(infos, taskInfo{
			ID:       t.ID,
			Title:    t.Title,
			Type:     string(t.Type),
			Status:   string(t.Status),
			WorkerID: t.WorkerID,
			Priority: int(t.Priority),
		})
		counts[string(t.Status)]++
	}

	result := map[string]interface{}{
		"tasks":          infos,
		"status_counts":  counts,
		"active_workers": q.pool.ActiveCount(),
		"phase":          string(q.phase),
	}

	b, _ := json.MarshalIndent(result, "", "  ")
	return string(b), nil
}

// ---------- get_task_output ----------

type getTaskOutputInput struct {
	TaskID string `json:"task_id"`
}

func handleGetTaskOutput(ctx context.Context, q *Queen, input json.RawMessage) (string, error) {
	var in getTaskOutputInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if in.TaskID == "" {
		return "", fmt.Errorf("task_id is required")
	}

	t, ok := q.tasks.Get(in.TaskID)
	if !ok {
		return "", fmt.Errorf("task %q not found", in.TaskID)
	}

	if t.Status != task.StatusComplete && t.Status != task.StatusFailed {
		return fmt.Sprintf("Task %q is still %s — no output yet.", in.TaskID, t.Status), nil
	}

	if t.Result != nil {
		var b strings.Builder
		fmt.Fprintf(&b, "Task: %s (%s)\nStatus: %s\nSuccess: %v\n", t.Title, t.ID, t.Status, t.Result.Success)
		if t.Result.Output != "" {
			fmt.Fprintf(&b, "Output:\n%s\n", t.Result.Output)
		}
		if len(t.Result.Errors) > 0 {
			fmt.Fprintf(&b, "Errors:\n")
			for _, e := range t.Result.Errors {
				fmt.Fprintf(&b, "  - %s\n", e)
			}
		}
		return b.String(), nil
	}

	// Fallback: check blackboard
	key := fmt.Sprintf("result-%s", in.TaskID)
	if entry, found := q.board.Read(key); found {
		if s, ok := entry.Value.(string); ok {
			return fmt.Sprintf("Task: %s (%s)\nStatus: %s\nOutput:\n%s", t.Title, t.ID, t.Status, s), nil
		}
	}

	return fmt.Sprintf("Task %q (%s) has no output available.", in.TaskID, t.Status), nil
}

// ---------- approve_task ----------

type approveTaskInput struct {
	TaskID   string `json:"task_id"`
	Feedback string `json:"feedback"`
}

func handleApproveTask(ctx context.Context, q *Queen, input json.RawMessage) (string, error) {
	var in approveTaskInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if in.TaskID == "" {
		return "", fmt.Errorf("task_id is required")
	}

	t, ok := q.tasks.Get(in.TaskID)
	if !ok {
		return "", fmt.Errorf("task %q not found", in.TaskID)
	}

	if in.Feedback != "" {
		q.board.Post(&blackboard.Entry{
			Key:      fmt.Sprintf("approval-%s", in.TaskID),
			Value:    in.Feedback,
			PostedBy: "queen",
			TaskID:   in.TaskID,
			Tags:     []string{"approval", "feedback"},
		})
	}

	return fmt.Sprintf("Task %q (%s) approved. Status: %s", in.TaskID, t.Title, t.Status), nil
}

// ---------- reject_task ----------

type rejectTaskInput struct {
	TaskID   string `json:"task_id"`
	Feedback string `json:"feedback"`
}

func handleRejectTask(ctx context.Context, q *Queen, input json.RawMessage) (string, error) {
	var in rejectTaskInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if in.TaskID == "" {
		return "", fmt.Errorf("task_id is required")
	}
	if in.Feedback == "" {
		return "", fmt.Errorf("feedback is required when rejecting a task")
	}

	t, ok := q.tasks.Get(in.TaskID)
	if !ok {
		return "", fmt.Errorf("task %q not found", in.TaskID)
	}

	if t.RetryCount >= t.MaxRetries {
		return "", fmt.Errorf("task %q has exhausted all retries (%d/%d)", in.TaskID, t.RetryCount, t.MaxRetries)
	}

	t.RetryCount++
	t.Description += "\n\nREJECTED (attempt " + fmt.Sprintf("%d/%d", t.RetryCount, t.MaxRetries) + "): " + in.Feedback

	if err := q.tasks.UpdateStatus(in.TaskID, task.StatusPending); err != nil {
		q.logger.Printf("⚠ Warning: failed to update task status: %v", err)
	}
	if err := q.db.UpdateTaskStatus(ctx, q.sessionID, in.TaskID, "pending"); err != nil {
		q.logger.Printf("⚠ Warning: failed to update task status: %v", err)
	}
	if err := q.db.UpdateTaskRetryCount(ctx, q.sessionID, in.TaskID, t.RetryCount); err != nil {
		q.logger.Printf("⚠ Warning: failed to update task retry count: %v", err)
	}

	// Post feedback to blackboard
	q.board.Post(&blackboard.Entry{
		Key:      fmt.Sprintf("rejection-%s-%d", in.TaskID, t.RetryCount),
		Value:    in.Feedback,
		PostedBy: "queen",
		TaskID:   in.TaskID,
		Tags:     []string{"rejection", "feedback"},
	})

	return fmt.Sprintf("Task %q rejected and re-queued (attempt %d/%d). Feedback appended to description.",
		in.TaskID, t.RetryCount, t.MaxRetries), nil
}

// ---------- wait_for_workers ----------

type waitForWorkersInput struct {
	TimeoutSeconds int `json:"timeout_seconds"`
}

func handleWaitForWorkers(ctx context.Context, q *Queen, input json.RawMessage) (string, error) {
	var in waitForWorkersInput
	_ = json.Unmarshal(input, &in) // ignore error; all fields optional

	timeoutSec := in.TimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = 300
	}

	// Snapshot current running task statuses
	runningBefore := map[string]task.Status{}
	q.mu.RLock()
	for _, taskID := range q.assignments {
		if t, ok := q.tasks.Get(taskID); ok {
			runningBefore[taskID] = t.Status
		}
	}
	q.mu.RUnlock()

	if len(q.pool.Active()) == 0 {
		return "No workers currently running.", nil
	}

	timer := time.NewTimer(time.Duration(timeoutSec) * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
			return "Timeout reached. No workers completed during the wait period.", nil
		case <-ticker.C:
			// Check if any task changed status
			var changed []string
			for taskID, oldStatus := range runningBefore {
				if t, ok := q.tasks.Get(taskID); ok {
					if t.Status != oldStatus {
						changed = append(changed, fmt.Sprintf("%s: %s -> %s", taskID, oldStatus, t.Status))
					}
				}
			}
			if len(changed) > 0 {
				// Also process results for completed workers
				q.processWorkerResults(ctx)
				var b strings.Builder
				fmt.Fprintf(&b, "%d task(s) changed status:\n", len(changed))
				for _, c := range changed {
					fmt.Fprintf(&b, "  - %s\n", c)
				}
				fmt.Fprintf(&b, "Active workers remaining: %d", q.pool.ActiveCount())
				return b.String(), nil
			}

			// Also check if active count decreased (worker finished but not in assignments)
			if len(q.pool.Active()) == 0 {
				q.processWorkerResults(ctx)
				return "All workers have finished.", nil
			}
		}
	}
}

// processWorkerResults collects results from completed workers and updates task state.
// This is used by wait_for_workers to ensure results are captured.
func (q *Queen) processWorkerResults(ctx context.Context) {
	q.mu.RLock()
	assignmentsCopy := make(map[string]string, len(q.assignments))
	for k, v := range q.assignments {
		assignmentsCopy[k] = v
	}
	q.mu.RUnlock()

	for workerID, taskID := range assignmentsCopy {
		bee, ok := q.pool.Get(workerID)
		if !ok {
			continue
		}

		status := bee.Monitor()
		if status != worker.StatusComplete && status != worker.StatusFailed {
			continue
		}

		result := bee.Result()
		if status == worker.StatusComplete && result != nil && result.Success {
			if err := q.tasks.UpdateStatus(taskID, task.StatusComplete); err != nil {
				q.logger.Printf("⚠ Warning: failed to update task status: %v", err)
			}
			t, _ := q.tasks.Get(taskID)
			if t != nil {
				t.Result = result
			}
			if err := q.db.UpdateTaskStatus(ctx, q.sessionID, taskID, "complete"); err != nil {
				q.logger.Printf("⚠ Warning: failed to update task status: %v", err)
			}
			if err := q.db.UpdateTaskResult(ctx, q.sessionID, taskID, result); err != nil {
				q.logger.Printf("⚠ Warning: failed to update task result: %v", err)
			}

			// Post to blackboard
			bbKey := fmt.Sprintf("result-%s", taskID)
			q.board.Post(&blackboard.Entry{
				Key:      bbKey,
				Value:    result.Output,
				PostedBy: workerID,
				TaskID:   taskID,
				Tags:     []string{"result"},
			})
		} else {
			// Use shared failure handling for error classification, retry, and backoff
			q.handleTaskFailure(ctx, taskID, workerID, result)
		}

		q.mu.Lock()
		delete(q.assignments, workerID)
		q.mu.Unlock()
	}
}

// ---------- read_file ----------

type readFileInput struct {
	Path      string `json:"path"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
}

func handleReadFile(ctx context.Context, q *Queen, input json.RawMessage) (string, error) {
	var in readFileInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if in.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	guard := q.guard
	if err := guard.CheckPath(in.Path); err != nil {
		return "", err
	}

	fullPath := in.Path
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(q.cfg.ProjectDir, fullPath)
	}

	if err := guard.CheckFileSize(fullPath); err != nil {
		return "", err
	}

	f, err := os.Open(fullPath)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	if in.LineStart > 0 || in.LineEnd > 0 {
		// Read specific lines
		scanner := bufio.NewScanner(f)
		var lines []string
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			if in.LineStart > 0 && lineNum < in.LineStart {
				continue
			}
			if in.LineEnd > 0 && lineNum > in.LineEnd {
				break
			}
			lines = append(lines, fmt.Sprintf("%4d | %s", lineNum, scanner.Text()))
		}
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("read file: %w", err)
		}
		return strings.Join(lines, "\n"), nil
	}

	// Read entire file
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	return string(data), nil
}

// ---------- list_files ----------

type listFilesInput struct {
	Path    string `json:"path"`
	Pattern string `json:"pattern"`
}

func handleListFiles(ctx context.Context, q *Queen, input json.RawMessage) (string, error) {
	var in listFilesInput
	_ = json.Unmarshal(input, &in) // all fields optional

	dir := in.Path
	if dir == "" {
		dir = "."
	}

	guard := q.guard
	if err := guard.CheckPath(dir); err != nil {
		return "", err
	}

	fullDir := dir
	if !filepath.IsAbs(fullDir) {
		fullDir = filepath.Join(q.cfg.ProjectDir, fullDir)
	}

	var files []string
	var walkErr error
	if in.Pattern != "" {
		// Walk with pattern matching
		walkErr = filepath.Walk(fullDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // skip errors
			}
			matched, _ := filepath.Match(in.Pattern, info.Name())
			if matched {
				rel, _ := filepath.Rel(q.cfg.ProjectDir, path)
				if info.IsDir() {
					files = append(files, rel+"/")
				} else {
					files = append(files, rel)
				}
			}
			return nil
		})
	} else {
		// Simple directory listing
		entries, readErr := os.ReadDir(fullDir)
		if readErr != nil {
			return "", fmt.Errorf("read directory: %w", readErr)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				name += "/"
			}
			files = append(files, name)
		}
	}
	if walkErr != nil {
		return "", fmt.Errorf("walk directory: %w", walkErr)
	}

	if len(files) == 0 {
		return "No files found.", nil
	}
	return strings.Join(files, "\n"), nil
}

// ---------- complete ----------

type completeInput struct {
	Summary string `json:"summary"`
}

func handleComplete(ctx context.Context, q *Queen, input json.RawMessage) (string, error) {
	var in completeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if in.Summary == "" {
		return "", fmt.Errorf("summary is required")
	}

	q.phase = PhaseDone
	if err := q.db.UpdateSessionStatus(ctx, q.sessionID, "done"); err != nil {
		q.logger.Printf("⚠ Warning: failed to update session status: %v", err)
	}

	return fmt.Sprintf("Objective marked as done. Summary: %s", in.Summary), nil
}

// ---------- fail ----------

type failInput struct {
	Reason string `json:"reason"`
}

func handleFail(ctx context.Context, q *Queen, input json.RawMessage) (string, error) {
	var in failInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if in.Reason == "" {
		return "", fmt.Errorf("reason is required")
	}

	q.phase = PhaseFailed
	if err := q.db.UpdateSessionStatus(ctx, q.sessionID, "failed"); err != nil {
		q.logger.Printf("⚠ Warning: failed to update session status: %v", err)
	}
	q.pool.KillAll()

	return fmt.Sprintf("Objective marked as failed. Reason: %s", in.Reason), nil
}
