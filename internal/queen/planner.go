package queen

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/HexSleeves/waggle/internal/task"
)

// taskIDCounter provides unique, collision-free task IDs.
var taskIDCounter atomic.Int64

// nextTaskID returns a unique task ID like "task-1700000000-1".
func nextTaskID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().Unix(), taskIDCounter.Add(1))
}

// plan decomposes the objective into tasks
func (q *Queen) plan(ctx context.Context) error {
	q.logVerbose("📋 Planning phase...")

	// Check if we already have tasks from a resumed session
	existing := q.tasks.All()
	if len(existing) > 0 {
		ready := q.tasks.Ready()
		q.logVerbose("  Resuming: %d total tasks, %d ready", len(existing), len(ready))
		return nil
	}

	// Check if pre-defined tasks were set (e.g., from --tasks flag or file)
	if len(q.pendingTasks) > 0 {
		for _, t := range q.pendingTasks {
			q.persistNewTask(ctx, t)
			q.logVerbose("  📌 Task: [%s] %s", t.Type, t.Title)
		}
		q.pendingTasks = nil
		return nil
	}

	// For non-AI adapters (exec), create a single direct task from the objective
	adapterName := q.router.Route(&task.Task{Type: task.TypeGeneric})
	if adapterName == "exec" {
		q.logVerbose("  Using exec adapter — treating objective as single task")
		t := &task.Task{
			ID:          nextTaskID("task"),
			Type:        task.TypeGeneric,
			Status:      task.StatusPending,
			Priority:    task.PriorityNormal,
			Title:       "Execute objective",
			Description: q.objective,
			MaxRetries:  q.cfg.Workers.MaxRetries,
			CreatedAt:   time.Now(),
			Timeout:     q.cfg.Workers.DefaultTimeout,
		}
		q.persistNewTask(ctx, t)
		q.logVerbose("  📌 Task: [%s] %s", t.Type, t.Title)
		return nil
	}

	// If Queen has her own LLM, use it directly for planning (no worker needed)
	if q.llm != nil {
		return q.planWithLLM(ctx)
	}

	// Fall back to worker-based planning
	return q.planWithWorker(ctx)
}

// planWithLLM uses the Queen's own LLM client to decompose the objective,
// avoiding the need to spawn a worker bee for planning.
func (q *Queen) planWithLLM(ctx context.Context) error {
	const systemPrompt = "You are a task planning agent. Output ONLY a JSON array of tasks."
	planPrompt := q.buildPlanPrompt()

	response, err := q.llm.Chat(ctx, systemPrompt, planPrompt)
	if err != nil {
		// Fall back to worker-based planning if LLM call fails
		q.logVerbose("  ⚠ Queen LLM planning failed: %v — falling back to worker", err)
		return q.planWithWorker(ctx)
	}

	tasks, err := q.parsePlanOutput(response)
	if err != nil {
		q.logVerbose("  ⚠ Failed to parse LLM plan output: %v — falling back to worker", err)
		return q.planWithWorker(ctx)
	}

	for _, t := range tasks {
		q.persistNewTask(ctx, t)
		q.logVerbose("  📌 Task: [%s] %s", t.Type, t.Title)
	}

	q.ctx.Add("assistant", fmt.Sprintf("Plan created with %d tasks", len(tasks)))
	return nil
}

// planWithWorker spawns a worker bee to decompose the objective using an AI adapter.
// This is the fallback path when the Queen's own LLM is not available or fails.
func (q *Queen) planWithWorker(ctx context.Context) error {
	if q.router == nil {
		return fmt.Errorf("no task router configured")
	}
	adapterName := q.router.Route(&task.Task{Type: task.TypeGeneric})

	planTask := &task.Task{
		ID:          nextTaskID("plan"),
		Type:        task.TypeGeneric,
		Status:      task.StatusPending,
		Priority:    task.PriorityCritical,
		Title:       "Decompose objective into tasks",
		Description: q.buildPlanPrompt(),
		CreatedAt:   time.Now(),
		Timeout:     q.cfg.Queen.PlanTimeout,
	}

	bee, err := q.pool.Spawn(ctx, planTask, adapterName)
	if err != nil {
		return fmt.Errorf("spawn planner: %w", err)
	}

	if err := q.waitForWorker(ctx, bee, planTask.Timeout); err != nil {
		return err
	}

	result := bee.Result()
	if result == nil || !result.Success {
		errMsg := "unknown error"
		if output := bee.Output(); output != "" {
			errMsg = output
		} else if result != nil && len(result.Errors) > 0 {
			errMsg = strings.Join(result.Errors, "; ")
		}
		return fmt.Errorf("planning failed: %s", errMsg)
	}

	tasks, err := q.parsePlanOutput(result.Output)
	if err != nil {
		return fmt.Errorf("parse plan: %w", err)
	}

	for _, t := range tasks {
		q.persistNewTask(ctx, t)
		q.logVerbose("  📌 Task: [%s] %s", t.Type, t.Title)
	}

	q.ctx.Add("assistant", fmt.Sprintf("Plan created with %d tasks", len(tasks)))
	return nil
}

// buildPlanPrompt creates the prompt for the planning phase
func (q *Queen) buildPlanPrompt() string {
	var b strings.Builder
	b.WriteString("You are a task planning agent. Decompose the following objective into discrete, parallelizable tasks.\n\n")
	b.WriteString(fmt.Sprintf("OBJECTIVE: %s\n\n", q.objective))

	b.WriteString("IMPORTANT PLANNING RULES:\n")
	b.WriteString("- Each task MUST be narrowly scoped to exactly one concern\n")
	b.WriteString("- Tasks MUST NOT make changes outside their described scope\n")
	b.WriteString("- Each task should include a \"constraints\" field listing what it must NOT do\n")
	b.WriteString("- Each task should include an \"allowed_paths\" field listing which files/dirs it may touch\n")
	b.WriteString("- Prefer more small focused tasks over fewer broad tasks\n")
	b.WriteString("- DO NOT combine unrelated changes into one task\n")
	b.WriteString(fmt.Sprintf("- MAXIMIZE PARALLELISM: we have %d workers. Only add depends_on when a task truly cannot start without another's output. Independent tasks MUST have empty depends_on so they run in parallel.\n", q.cfg.Workers.MaxParallel))
	b.WriteString("- DO NOT create linear chains of dependencies unless strictly necessary — if tasks touch different files, they are independent\n\n")

	b.WriteString("Output a JSON array of tasks. Each task should have:\n")
	b.WriteString(`- "id": unique string identifier` + "\n")
	b.WriteString(`- "type": one of "code", "research", "test", "review", "generic"` + "\n")
	b.WriteString(`- "title": short title` + "\n")
	b.WriteString(`- "description": detailed description of what to do (be specific and narrow)` + "\n")
	b.WriteString(`- "constraints": array of strings — things this task MUST NOT do (e.g., "Do not modify any files", "Only read, do not write", "Do not change function signatures", "Do not refactor code outside the described scope")` + "\n")
	b.WriteString(`- "allowed_paths": array of file/directory paths this task may read or modify (e.g., ["internal/queen/queen.go", "internal/queen/"])` + "\n")
	b.WriteString(`- "priority": 0-3 (0=low, 3=critical)` + "\n")
	b.WriteString(`- "depends_on": array of task IDs this depends on (empty if independent)` + "\n")
	b.WriteString(`- "max_retries": number of retries allowed (default 2)` + "\n")
	b.WriteString("\nOutput ONLY the JSON array, no other text.\n")

	// Include blackboard context if available
	if summary := q.board.Summarize(); summary != "Blackboard is empty." {
		b.WriteString("\nCurrent context from previous work:\n")
		b.WriteString(summary)
	}

	return b.String()
}

// parsePlanOutput extracts tasks from the planner's JSON output
func (q *Queen) parsePlanOutput(output string) ([]*task.Task, error) {
	// Try to find JSON array in the output
	output = strings.TrimSpace(output)

	// Find the JSON array
	start := strings.Index(output, "[")
	end := strings.LastIndex(output, "]")
	if start == -1 || end == -1 || end <= start {
		// No JSON array found — LLM returned garbage. Fall back to a single task
		// but log a warning so the issue is visible.
		q.logger.Printf("⚠ Plan output contained no JSON array — falling back to single task")
		return []*task.Task{{
			ID:          nextTaskID("task"),
			Type:        task.TypeGeneric,
			Status:      task.StatusPending,
			Priority:    task.PriorityNormal,
			Title:       "Execute objective",
			Description: q.objective,
			MaxRetries:  q.cfg.Workers.MaxRetries,
			CreatedAt:   time.Now(),
			Timeout:     q.cfg.Workers.DefaultTimeout,
		}}, nil
	}

	jsonStr := output[start : end+1]

	var rawTasks []map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &rawTasks); err != nil {
		return nil, fmt.Errorf("parse JSON tasks: %w", err)
	}

	tasks := make([]*task.Task, 0, len(rawTasks))
	for _, rt := range rawTasks {
		t := &task.Task{
			ID:           jsonStr_s(rt, "id"),
			Type:         task.Type(jsonStr_s(rt, "type")),
			Status:       task.StatusPending,
			Priority:     task.Priority(jsonInt(rt, "priority")),
			Title:        jsonStr_s(rt, "title"),
			Description:  jsonStr_s(rt, "description"),
			Constraints:  jsonStrSlice(rt, "constraints"),
			AllowedPaths: jsonStrSlice(rt, "allowed_paths"),
			DependsOn:    jsonStrSlice(rt, "depends_on"),
			MaxRetries:   jsonInt(rt, "max_retries"),
			CreatedAt:    time.Now(),
			Timeout:      q.cfg.Workers.DefaultTimeout,
		}
		if t.ID == "" {
			t.ID = nextTaskID("task")
		}
		if t.MaxRetries == 0 {
			t.MaxRetries = q.cfg.Workers.MaxRetries
		}
		tasks = append(tasks, t)
	}

	// Check for circular dependencies in the parsed tasks
	// Create a temporary task graph to validate dependencies
	tempGraph := task.NewTaskGraph(nil)
	for _, t := range tasks {
		tempGraph.Add(t)
	}
	if err := tempGraph.DetectCycles(); err != nil {
		return nil, err
	}

	return tasks, nil
}

// --- JSON parsing helpers (tolerant of LLM output variations) ---

func jsonStr_s(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return fmt.Sprintf("%v", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

func jsonInt(m map[string]interface{}, key string) int {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int(val)
	case string:
		// Handle "high", "medium", "low" or numeric strings
		switch strings.ToLower(val) {
		case "critical":
			return 3
		case "high":
			return 2
		case "medium", "normal":
			return 1
		case "low":
			return 0
		default:
			n, _ := strconv.Atoi(val)
			return n
		}
	default:
		return 0
	}
}

func jsonStrSlice(m map[string]interface{}, key string) []string {
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}
