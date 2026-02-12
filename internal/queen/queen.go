package queen

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/exedev/queen-bee/internal/adapter"
	"github.com/exedev/queen-bee/internal/blackboard"
	"github.com/exedev/queen-bee/internal/bus"
	"github.com/exedev/queen-bee/internal/compact"
	"github.com/exedev/queen-bee/internal/config"
	"github.com/exedev/queen-bee/internal/state"
	"github.com/exedev/queen-bee/internal/task"
	"github.com/exedev/queen-bee/internal/worker"
)

// Phase represents the current phase of the Queen's loop
type Phase string

const (
	PhasePlan     Phase = "plan"
	PhaseDelegate Phase = "delegate"
	PhaseMonitor  Phase = "monitor"
	PhaseReview   Phase = "review"
	PhaseDone     Phase = "done"
	PhaseFailed   Phase = "failed"
)

// Queen is the central orchestrator agent
type Queen struct {
	mu sync.RWMutex

	cfg        *config.Config
	bus        *bus.MessageBus
	store      *state.Store
	board      *blackboard.Blackboard
	tasks      *task.TaskGraph
	pool       *worker.Pool
	router     *adapter.TaskRouter
	registry   *adapter.Registry
	ctx        *compact.Context

	phase      Phase
	objective  string
	iteration  int
	logger     *log.Logger

	// For tracking worker->task assignments
	assignments map[string]string // workerID -> taskID
}

// New creates a new Queen orchestrator
func New(cfg *config.Config, logger *log.Logger) (*Queen, error) {
	if logger == nil {
		logger = log.Default()
	}

	// Initialize message bus
	msgBus := bus.New(10000)

	// Initialize state store
	store, err := state.NewStore(cfg.HivePath())
	if err != nil {
		return nil, fmt.Errorf("init state store: %w", err)
	}

	// Initialize blackboard
	board := blackboard.New(msgBus)

	// Initialize task graph
	tasks := task.NewTaskGraph(msgBus)

	// Initialize adapter registry
	registry := adapter.NewRegistry()
	registry.Register(adapter.NewClaudeAdapter(
		cfg.Adapters["claude-code"].Command,
		cfg.Adapters["claude-code"].Args,
		cfg.ProjectDir,
	))
	registry.Register(adapter.NewCodexAdapter(
		cfg.Adapters["codex"].Command,
		cfg.Adapters["codex"].Args,
		cfg.ProjectDir,
	))
	registry.Register(adapter.NewOpenCodeAdapter(
		cfg.Adapters["opencode"].Command,
		cfg.Adapters["opencode"].Args,
		cfg.ProjectDir,
	))

	router := adapter.NewTaskRouter(registry)

	// Initialize worker pool
	pool := worker.NewPool(
		cfg.Workers.MaxParallel,
		registry.WorkerFactory(),
		msgBus,
	)

	// Context management
	ctxMgr := compact.NewContext(200000) // ~200k tokens

	q := &Queen{
		cfg:         cfg,
		bus:         msgBus,
		store:       store,
		board:       board,
		tasks:       tasks,
		pool:        pool,
		router:      router,
		registry:    registry,
		ctx:         ctxMgr,
		phase:       PhasePlan,
		logger:      logger,
		assignments: make(map[string]string),
	}

	// Wire up event logging
	msgBus.SubscribeAll(func(msg bus.Message) {
		store.Append(string(msg.Type), msg)
	})

	return q, nil
}

// Run executes the Queen's main loop: Plan -> Delegate -> Monitor -> Review
func (q *Queen) Run(ctx context.Context, objective string) error {
	q.objective = objective
	q.logger.Printf("🐝 Queen Bee starting | Objective: %s", objective)

	q.store.Append("queen.start", map[string]string{
		"objective": objective,
	})

	for q.iteration = 0; q.iteration < q.cfg.Queen.MaxIterations; q.iteration++ {
		select {
		case <-ctx.Done():
			q.logger.Println("⛔ Context cancelled, shutting down")
			q.pool.KillAll()
			return ctx.Err()
		default:
		}

		q.logger.Printf("\n━━━ Iteration %d | Phase: %s ━━━", q.iteration+1, q.phase)

		switch q.phase {
		case PhasePlan:
			if err := q.plan(ctx); err != nil {
				q.logger.Printf("❌ Plan failed: %v", err)
				q.phase = PhaseFailed
				continue
			}
			q.phase = PhaseDelegate

		case PhaseDelegate:
			if err := q.delegate(ctx); err != nil {
				q.logger.Printf("❌ Delegate failed: %v", err)
				q.phase = PhaseFailed
				continue
			}
			q.phase = PhaseMonitor

		case PhaseMonitor:
			if err := q.monitor(ctx); err != nil {
				q.logger.Printf("❌ Monitor failed: %v", err)
				q.phase = PhaseFailed
				continue
			}
			q.phase = PhaseReview

		case PhaseReview:
			done, err := q.review(ctx)
			if err != nil {
				q.logger.Printf("❌ Review failed: %v", err)
				q.phase = PhaseFailed
				continue
			}
			if done {
				q.phase = PhaseDone
			} else {
				q.phase = PhasePlan // Loop back for more work
			}

		case PhaseDone:
			q.logger.Println("✅ All tasks complete!")
			q.store.Append("queen.done", q.buildSummary())
			return nil

		case PhaseFailed:
			return q.handleFailure(ctx)
		}

		// Compact context if needed
		if q.ctx.NeedsCompaction() {
			q.logger.Println("📦 Compacting context...")
			q.ctx.Compact(compact.DefaultSummarizer)
		}
	}

	return fmt.Errorf("max iterations (%d) reached", q.cfg.Queen.MaxIterations)
}

// plan decomposes the objective into tasks
func (q *Queen) plan(ctx context.Context) error {
	q.logger.Println("📋 Planning phase...")

	// Check if we already have tasks from a resumed session
	existing := q.tasks.All()
	if len(existing) > 0 {
		ready := q.tasks.Ready()
		q.logger.Printf("  Resuming: %d total tasks, %d ready", len(existing), len(ready))
		return nil
	}

	// Decompose the objective into tasks using the configured worker
	planTask := &task.Task{
		ID:          fmt.Sprintf("plan-%d", time.Now().UnixNano()),
		Type:        task.TypeGeneric,
		Status:      task.StatusPending,
		Priority:    task.PriorityCritical,
		Title:       "Decompose objective into tasks",
		Description: q.buildPlanPrompt(),
		CreatedAt:   time.Now(),
		Timeout:     q.cfg.Queen.PlanTimeout,
	}

	adapterName := q.router.Route(planTask)
	if adapterName == "" {
		return fmt.Errorf("no adapter available for planning")
	}

	bee, err := q.pool.Spawn(ctx, planTask, adapterName)
	if err != nil {
		return fmt.Errorf("spawn planner: %w", err)
	}

	// Wait for planning to complete
	if err := q.waitForWorker(ctx, bee, planTask.Timeout); err != nil {
		return err
	}

	result := bee.Result()
	if result == nil || !result.Success {
		errMsg := "unknown error"
		if result != nil && len(result.Errors) > 0 {
			errMsg = strings.Join(result.Errors, "; ")
		}
		return fmt.Errorf("planning failed: %s", errMsg)
	}

	// Parse the plan output into tasks
	tasks, err := q.parsePlanOutput(result.Output)
	if err != nil {
		return fmt.Errorf("parse plan: %w", err)
	}

	for _, t := range tasks {
		q.tasks.Add(t)
		q.logger.Printf("  📌 Task: [%s] %s", t.Type, t.Title)
	}

	q.store.Append("queen.plan", map[string]interface{}{
		"task_count": len(tasks),
		"tasks":      tasks,
	})

	q.ctx.Add("assistant", fmt.Sprintf("Plan created with %d tasks", len(tasks)))

	return nil
}

// delegate assigns ready tasks to workers
func (q *Queen) delegate(ctx context.Context) error {
	q.logger.Println("📤 Delegation phase...")

	ready := q.tasks.Ready()
	if len(ready) == 0 {
		q.logger.Println("  No tasks ready (all have unmet dependencies)")
		return nil
	}

	// Sort by priority
	sort.Slice(ready, func(i, j int) bool {
		return ready[i].Priority > ready[j].Priority
	})

	for _, t := range ready {
		if q.pool.ActiveCount() >= q.cfg.Workers.MaxParallel {
			q.logger.Printf("  ⏸ Max parallel workers reached, queuing remaining")
			break
		}

		adapterName := q.router.Route(t)
		if adapterName == "" {
			q.logger.Printf("  ⚠ No adapter for task %s, skipping", t.ID)
			continue
		}

		bee, err := q.pool.Spawn(ctx, t, adapterName)
		if err != nil {
			q.logger.Printf("  ⚠ Failed to spawn worker for %s: %v", t.ID, err)
			continue
		}

		q.mu.Lock()
		q.assignments[bee.ID()] = t.ID
		q.mu.Unlock()

		q.tasks.UpdateStatus(t.ID, task.StatusRunning)
		t.WorkerID = bee.ID()

		q.logger.Printf("  🐝 Assigned [%s] %s -> %s (%s)", t.Type, t.Title, bee.ID(), adapterName)

		q.store.Append("queen.delegate", map[string]string{
			"task_id":   t.ID,
			"worker_id": bee.ID(),
			"adapter":   adapterName,
		})
	}

	return nil
}

// monitor watches running workers until all complete or fail
func (q *Queen) monitor(ctx context.Context) error {
	q.logger.Println("👁 Monitoring phase...")

	pollInterval := 2 * time.Second
	timeout := q.cfg.Workers.DefaultTimeout
	deadline := time.Now().Add(timeout)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if time.Now().After(deadline) {
			q.logger.Println("  ⏰ Monitoring timeout reached, killing stuck workers")
			q.pool.KillAll()
			return nil
		}

		active := q.pool.Active()
		if len(active) == 0 {
			q.logger.Println("  ✓ All workers finished")
			return nil
		}

		for _, bee := range active {
			status := bee.Monitor()
			q.logger.Printf("  [%s] status: %s", bee.ID(), status)
		}

		time.Sleep(pollInterval)
	}
}

// review examines completed work, handles failures
func (q *Queen) review(ctx context.Context) (bool, error) {
	q.logger.Println("🔍 Review phase...")

	// Process results from assignments
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

		switch bee.Monitor() {
		case worker.StatusComplete:
			result := bee.Result()
			if result != nil && result.Success {
				q.tasks.UpdateStatus(taskID, task.StatusComplete)
				t, _ := q.tasks.Get(taskID)
				if t != nil {
					t.Result = result
				}

				// Post result to blackboard
				q.board.Post(&blackboard.Entry{
					Key:      fmt.Sprintf("result-%s", taskID),
					Value:    result.Output,
					PostedBy: workerID,
					TaskID:   taskID,
					Tags:     []string{"result", string(t.Type)},
				})

				q.logger.Printf("  ✅ Task %s completed by %s", taskID, workerID)
				q.ctx.Add("assistant", fmt.Sprintf("Task %s completed: %s", taskID, truncate(result.Output, 500)))
			} else {
				q.handleTaskFailure(ctx, taskID, workerID, result)
			}

		case worker.StatusFailed:
			result := bee.Result()
			q.handleTaskFailure(ctx, taskID, workerID, result)
		}
	}

	// Clean up finished workers
	q.pool.Cleanup()

	// Check if all tasks are done
	if q.tasks.AllComplete() {
		return true, nil
	}

	// Check if there are more ready tasks
	ready := q.tasks.Ready()
	if len(ready) > 0 {
		q.logger.Printf("  📋 %d more tasks ready", len(ready))
		return false, nil
	}

	// Check for deadlock (no tasks running, none ready, not all complete)
	if q.pool.ActiveCount() == 0 && len(ready) == 0 && !q.tasks.AllComplete() {
		failed := q.tasks.Failed()
		if len(failed) > 0 {
			q.logger.Printf("  💀 %d tasks failed with no recovery possible", len(failed))
			return false, fmt.Errorf("%d tasks failed", len(failed))
		}
	}

	return false, nil
}

// handleTaskFailure manages retry logic for failed tasks
func (q *Queen) handleTaskFailure(ctx context.Context, taskID, workerID string, result *task.Result) {
	t, ok := q.tasks.Get(taskID)
	if !ok {
		return
	}

	errMsg := "unknown error"
	if result != nil && len(result.Errors) > 0 {
		errMsg = strings.Join(result.Errors, "; ")
	}

	q.logger.Printf("  ❌ Task %s failed: %s", taskID, truncate(errMsg, 200))

	if t.RetryCount < t.MaxRetries {
		t.RetryCount++
		q.tasks.UpdateStatus(taskID, task.StatusPending)
		q.logger.Printf("  🔄 Retrying task %s (attempt %d/%d)", taskID, t.RetryCount, t.MaxRetries)

		q.store.Append("queen.retry", map[string]interface{}{
			"task_id":     taskID,
			"retry_count": t.RetryCount,
			"error":       errMsg,
		})
	} else {
		q.tasks.UpdateStatus(taskID, task.StatusFailed)
		q.store.Append("queen.task_failed", map[string]interface{}{
			"task_id": taskID,
			"error":   errMsg,
		})
	}
}

// handleFailure is the top-level failure handler
func (q *Queen) handleFailure(ctx context.Context) error {
	q.pool.KillAll()

	failed := q.tasks.Failed()
	var errs []string
	for _, t := range failed {
		errs = append(errs, fmt.Sprintf("[%s] %s", t.ID, t.Title))
	}

	q.store.Append("queen.failed", map[string]interface{}{
		"failed_tasks": errs,
	})

	return fmt.Errorf("queen failed: %d tasks could not be completed: %s",
		len(failed), strings.Join(errs, ", "))
}

// buildPlanPrompt creates the prompt for the planning phase
func (q *Queen) buildPlanPrompt() string {
	var b strings.Builder
	b.WriteString("You are a task planning agent. Decompose the following objective into discrete, parallelizable tasks.\n\n")
	b.WriteString(fmt.Sprintf("OBJECTIVE: %s\n\n", q.objective))
	b.WriteString("Output a JSON array of tasks. Each task should have:\n")
	b.WriteString(`- "id": unique string identifier\n`)
	b.WriteString(`- "type": one of "code", "research", "test", "review", "generic"\n`)
	b.WriteString(`- "title": short title\n`)
	b.WriteString(`- "description": detailed description of what to do\n`)
	b.WriteString(`- "priority": 0-3 (0=low, 3=critical)\n`)
	b.WriteString(`- "depends_on": array of task IDs this depends on (empty if independent)\n`)
	b.WriteString(`- "max_retries": number of retries allowed (default 2)\n`)
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
		// Fallback: create a single task from the output
		return []*task.Task{{
			ID:          fmt.Sprintf("task-%d", time.Now().UnixNano()),
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

	var rawTasks []struct {
		ID          string   `json:"id"`
		Type        string   `json:"type"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Priority    int      `json:"priority"`
		DependsOn   []string `json:"depends_on"`
		MaxRetries  int      `json:"max_retries"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &rawTasks); err != nil {
		return nil, fmt.Errorf("parse JSON tasks: %w", err)
	}

	tasks := make([]*task.Task, 0, len(rawTasks))
	for _, rt := range rawTasks {
		t := &task.Task{
			ID:          rt.ID,
			Type:        task.Type(rt.Type),
			Status:      task.StatusPending,
			Priority:    task.Priority(rt.Priority),
			Title:       rt.Title,
			Description: rt.Description,
			DependsOn:   rt.DependsOn,
			MaxRetries:  rt.MaxRetries,
			CreatedAt:   time.Now(),
			Timeout:     q.cfg.Workers.DefaultTimeout,
		}
		if t.MaxRetries == 0 {
			t.MaxRetries = q.cfg.Workers.MaxRetries
		}
		tasks = append(tasks, t)
	}

	return tasks, nil
}

// buildSummary creates a completion summary
func (q *Queen) buildSummary() map[string]interface{} {
	allTasks := q.tasks.All()
	completed := 0
	for _, t := range allTasks {
		if t.Status == task.StatusComplete {
			completed++
		}
	}

	return map[string]interface{}{
		"objective":       q.objective,
		"total_tasks":     len(allTasks),
		"completed_tasks": completed,
		"iterations":      q.iteration + 1,
		"blackboard_keys": q.board.Keys(),
	}
}

// Close cleans up resources
func (q *Queen) Close() error {
	q.pool.KillAll()
	return q.store.Close()
}

// Status returns the current queen status for display
func (q *Queen) Status() map[string]interface{} {
	q.mu.RLock()
	defer q.mu.RUnlock()

	return map[string]interface{}{
		"phase":          q.phase,
		"iteration":      q.iteration,
		"objective":      q.objective,
		"active_workers": q.pool.ActiveCount(),
		"total_tasks":    len(q.tasks.All()),
		"ready_tasks":    len(q.tasks.Ready()),
		"context_tokens": q.ctx.TokenCount(),
	}
}

// waitForWorker blocks until a worker completes or times out
func (q *Queen) waitForWorker(ctx context.Context, bee worker.Bee, timeout time.Duration) error {
	if timeout == 0 {
		timeout = q.cfg.Workers.DefaultTimeout
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			bee.Kill()
			return ctx.Err()
		case <-deadline.C:
			bee.Kill()
			return fmt.Errorf("worker %s timed out after %v", bee.ID(), timeout)
		case <-ticker.C:
			status := bee.Monitor()
			if status == worker.StatusComplete || status == worker.StatusFailed {
				return nil
			}
		}
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
