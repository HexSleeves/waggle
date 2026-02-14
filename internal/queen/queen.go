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
	"github.com/exedev/queen-bee/internal/errors"
	"github.com/exedev/queen-bee/internal/safety"
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

	cfg      *config.Config
	bus      *bus.MessageBus
	store    *state.Store
	db       *state.DB
	board    *blackboard.Blackboard
	tasks    *task.TaskGraph
	pool     *worker.Pool
	router   *adapter.TaskRouter
	registry *adapter.Registry
	ctx      *compact.Context

	phase     Phase
	objective string
	sessionID string
	iteration int
	logger    *log.Logger
	lastErr   error

	// For tracking worker->task assignments
	assignments  map[string]string // workerID -> taskID
	pendingTasks []*task.Task      // pre-defined tasks (skip AI planning)
}

// SetTasks allows pre-defining tasks, skipping AI-based planning
func (q *Queen) SetTasks(tasks []*task.Task) {
	q.pendingTasks = tasks
}

// ResumeSession loads a previous session's tasks from the database.
// It restores the task graph with all statuses, and prepares the queen
// to continue execution from the appropriate phase.
// Returns the objective and any error encountered.
func (q *Queen) ResumeSession(sessionID string) (string, error) {
	// Get session info including phase and iteration
	sessionInfo, err := q.db.GetSessionFull(sessionID)
	if err != nil {
		return "", fmt.Errorf("get session info: %w", err)
	}

	q.sessionID = sessionID
	q.objective = sessionInfo.Objective

	// Restore phase and iteration if available
	if sessionInfo.Phase != "" {
		q.phase = Phase(sessionInfo.Phase)
	}
	if sessionInfo.Iteration > 0 {
		q.iteration = sessionInfo.Iteration
	}

	// Reset any 'running' tasks to 'pending' since workers are gone
	if err := q.db.ResetRunningTasks(sessionID); err != nil {
		q.logger.Printf("⚠ Warning: failed to reset running tasks: %v", err)
	}

	// Load all tasks from the session
	taskRows, err := q.db.GetTasks(sessionID)
	if err != nil {
		return "", fmt.Errorf("load tasks: %w", err)
	}

	// Convert TaskRows to Tasks and add to task graph
	for _, tr := range taskRows {
		t := taskFromRow(&tr)
		q.tasks.Add(t)
		q.logger.Printf("  📋 Restored task: [%s] %s (%s)", t.Type, t.Title, t.Status)
	}

	q.logger.Printf("🔄 Resumed session %s with %d tasks", sessionID, len(taskRows))
	q.logger.Printf("   Objective: %s", q.objective)
	q.logger.Printf("   Phase: %s | Iteration: %d", q.phase, q.iteration)

	return q.objective, nil
}

// taskFromRow converts a state.TaskRow to a task.Task
func taskFromRow(tr *state.TaskRow) *task.Task {
	// Parse depends_on from comma-separated string
	var dependsOn []string
	if tr.DependsOn != "" {
		dependsOn = strings.Split(tr.DependsOn, ",")
	}

	t := &task.Task{
		ID:          tr.ID,
		Type:        task.Type(tr.Type),
		Status:      task.Status(tr.Status),
		Priority:    task.Priority(tr.Priority),
		Title:       tr.Title,
		Description: tr.Description,
		MaxRetries:  tr.MaxRetries,
		RetryCount:  tr.RetryCount,
		DependsOn:   dependsOn,
	}

	if tr.WorkerID != nil {
		t.WorkerID = *tr.WorkerID
	}

	// Parse timestamps if present
	if tr.CreatedAt != "" {
		if ts, err := time.Parse(time.RFC3339Nano, tr.CreatedAt); err == nil {
			t.CreatedAt = ts
		} else {
			t.CreatedAt = time.Now()
		}
	} else {
		t.CreatedAt = time.Now()
	}

	if tr.StartedAt != nil && *tr.StartedAt != "" {
		if ts, err := time.Parse(time.RFC3339Nano, *tr.StartedAt); err == nil {
			t.StartedAt = &ts
		}
	}

	if tr.CompletedAt != nil && *tr.CompletedAt != "" {
		if ts, err := time.Parse(time.RFC3339Nano, *tr.CompletedAt); err == nil {
			t.CompletedAt = &ts
		}
	}

	// Parse result if present
	if tr.Result != nil && *tr.Result != "" {
		var result task.Result
		if err := json.Unmarshal([]byte(*tr.Result), &result); err == nil {
			t.Result = &result
		}
	}

	// Restore last_error_type from result_data if present
	if tr.ResultData != nil && *tr.ResultData != "" {
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(*tr.ResultData), &data); err == nil {
			if et, ok := data["last_error_type"].(string); ok {
				t.LastErrorType = et
			}
		}
	}

	return t
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

	// Initialize SQLite DB (additional persistence layer)
	db, err := state.OpenDB(cfg.HivePath())
	if err != nil {
		return nil, fmt.Errorf("init state db: %w", err)
	}

	// Initialize blackboard
	board := blackboard.New(msgBus)

	// Initialize task graph
	tasks := task.NewTaskGraph(msgBus)

	// Initialize safety guard
	guard, err := safety.NewGuard(cfg.Safety, cfg.ProjectDir)
	if err != nil {
		return nil, fmt.Errorf("init safety guard: %w", err)
	}

	// Initialize adapter registry
	registry := adapter.NewRegistry()
	registry.Register(adapter.NewClaudeAdapter(
		cfg.Adapters["claude-code"].Command,
		cfg.Adapters["claude-code"].Args,
		cfg.ProjectDir,
		guard,
	))
	registry.Register(adapter.NewCodexAdapter(
		cfg.Adapters["codex"].Command,
		cfg.Adapters["codex"].Args,
		cfg.ProjectDir,
		guard,
	))
	registry.Register(adapter.NewOpenCodeAdapter(
		cfg.Adapters["opencode"].Command,
		cfg.Adapters["opencode"].Args,
		cfg.ProjectDir,
		guard,
	))

	// Register exec adapter (always available fallback)
	registry.Register(adapter.NewExecAdapter(cfg.ProjectDir, guard))

	// Register kimi adapter
	registry.Register(adapter.NewKimiAdapter(
		cfg.Adapters["kimi"].Command,
		cfg.Adapters["kimi"].Args,
		cfg.ProjectDir,
		guard,
	))

	// Register gemini adapter
	registry.Register(adapter.NewGeminiAdapter(
		cfg.Adapters["gemini"].Command,
		cfg.Adapters["gemini"].Args,
		cfg.ProjectDir,
		guard,
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
		db:          db,
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
		if sid := q.sessionID; sid != "" {
			q.db.AppendEvent(sid, string(msg.Type), msg)
		}
	})

	return q, nil
}

// Run executes the Queen's main loop: Plan -> Delegate -> Monitor -> Review
func (q *Queen) Run(ctx context.Context, objective string) error {
	q.objective = objective
	q.logger.Printf("🐝 Queen Bee starting | Objective: %s", objective)

	// Preflight: verify the default adapter is available
	available := q.registry.Available()
	if len(available) == 0 {
		return fmt.Errorf("no adapters available — install claude, codex, or opencode CLI")
	}
	defaultAdapter := q.cfg.Workers.DefaultAdapter
	allTypes := []task.Type{task.TypeCode, task.TypeResearch, task.TypeTest, task.TypeReview, task.TypeGeneric}
	if a, ok := q.registry.Get(defaultAdapter); !ok || !a.Available() {
		q.logger.Printf("⚠ Default adapter %q not available, falling back to: %s", defaultAdapter, available[0])
		for _, tt := range allTypes {
			q.router.SetRoute(tt, available[0])
		}
	} else {
		q.logger.Printf("✓ Using adapter: %s", defaultAdapter)
		// Ensure all task types route to the user's chosen adapter
		for _, tt := range allTypes {
			q.router.SetRoute(tt, defaultAdapter)
		}
	}
	q.logger.Printf("✓ Available adapters: %v", available)

	// Create DB session
	q.sessionID = fmt.Sprintf("session-%d", time.Now().UnixNano())
	if err := q.db.CreateSession(q.sessionID, objective); err != nil {
		q.logger.Printf("⚠ DB: failed to create session: %v", err)
	}

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
				q.lastErr = err
				q.phase = PhaseFailed
				q.savePhase()
				continue
			}
			q.phase = PhaseDelegate
			q.savePhase()

		case PhaseDelegate:
			if err := q.delegate(ctx); err != nil {
				q.logger.Printf("❌ Delegate failed: %v", err)
				q.phase = PhaseFailed
				q.savePhase()
				continue
			}
			q.phase = PhaseMonitor
			q.savePhase()

		case PhaseMonitor:
			if err := q.monitor(ctx); err != nil {
				q.logger.Printf("❌ Monitor failed: %v", err)
				q.phase = PhaseFailed
				q.savePhase()
				continue
			}
			q.phase = PhaseReview
			q.savePhase()

		case PhaseReview:
			done, err := q.review(ctx)
			if err != nil {
				q.logger.Printf("❌ Review failed: %v", err)
				q.phase = PhaseFailed
				q.savePhase()
				continue
			}
			if done {
				q.phase = PhaseDone
			} else {
				q.phase = PhasePlan // Loop back for more work
			}
			q.savePhase()

		case PhaseDone:
			q.logger.Println("✅ All tasks complete!")
			q.store.Append("queen.done", q.buildSummary())
			q.db.UpdateSessionStatus(q.sessionID, "done")
			q.printReport()
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

	// Check if pre-defined tasks were set (e.g., from --tasks flag or file)
	if len(q.pendingTasks) > 0 {
		for _, t := range q.pendingTasks {
			q.tasks.Add(t)
			q.db.InsertTask(q.sessionID, state.TaskRow{
				ID: t.ID, Type: string(t.Type), Status: string(t.Status),
				Priority: int(t.Priority), Title: t.Title, Description: t.Description,
				MaxRetries: t.MaxRetries, DependsOn: strings.Join(t.DependsOn, ","),
			})
			q.logger.Printf("  📌 Task: [%s] %s", t.Type, t.Title)
		}
		q.pendingTasks = nil
		return nil
	}

	// For non-AI adapters (exec), create a single direct task from the objective
	adapterName := q.router.Route(&task.Task{Type: task.TypeGeneric})
	if adapterName == "exec" {
		q.logger.Println("  Using exec adapter — treating objective as single task")
		t := &task.Task{
			ID:          fmt.Sprintf("task-%d", time.Now().UnixNano()),
			Type:        task.TypeGeneric,
			Status:      task.StatusPending,
			Priority:    task.PriorityNormal,
			Title:       "Execute objective",
			Description: q.objective,
			MaxRetries:  q.cfg.Workers.MaxRetries,
			CreatedAt:   time.Now(),
			Timeout:     q.cfg.Workers.DefaultTimeout,
		}
		q.tasks.Add(t)
		q.db.InsertTask(q.sessionID, state.TaskRow{
			ID: t.ID, Type: string(t.Type), Status: string(t.Status),
			Priority: int(t.Priority), Title: t.Title, Description: t.Description,
			MaxRetries: t.MaxRetries, DependsOn: strings.Join(t.DependsOn, ","),
		})
		q.logger.Printf("  📌 Task: [%s] %s", t.Type, t.Title)
		return nil
	}

	// Use AI adapter to decompose the objective
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
		if output := bee.Output(); output != "" {
			errMsg = output
		} else if result != nil && len(result.Errors) > 0 {
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
		q.db.InsertTask(q.sessionID, state.TaskRow{
			ID: t.ID, Type: string(t.Type), Status: string(t.Status),
			Priority: int(t.Priority), Title: t.Title, Description: t.Description,
			MaxRetries: t.MaxRetries, DependsOn: strings.Join(t.DependsOn, ","),
		})
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

		// Inject default scope constraints into every task
		t.Constraints = appendUnique(t.Constraints,
			"Do NOT make changes outside the scope described in this task",
			"Do NOT refactor, reorganize, or 'improve' code unrelated to this task",
			"Do NOT modify function signatures unless explicitly asked to",
			"If you find issues outside your scope, note them in your output but do NOT fix them",
		)

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

		q.db.UpdateTaskStatus(q.sessionID, t.ID, "running")
		q.db.UpdateTaskWorker(q.sessionID, t.ID, bee.ID())

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
	pollCount := 0

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

		if pollCount%5 == 0 { // Log every 10s instead of every 2s
			q.logger.Printf("  ⏳ %d workers active...", len(active))
		}
		pollCount++

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

				q.db.UpdateTaskStatus(q.sessionID, taskID, "complete")
				q.db.UpdateTaskResult(q.sessionID, taskID, result)

				// Post result to blackboard
				bbKey := fmt.Sprintf("result-%s", taskID)
				var tagsStr string
				if t != nil {
					tagsStr = strings.Join([]string{"result", string(t.Type)}, ",")
				}
				q.board.Post(&blackboard.Entry{
					Key:      bbKey,
					Value:    result.Output,
					PostedBy: workerID,
					TaskID:   taskID,
					Tags:     []string{"result", string(t.Type)},
				})
				q.db.PostBlackboard(q.sessionID, bbKey, result.Output, workerID, taskID, tagsStr)

				q.logger.Printf("  ✅ Task %s completed by %s", taskID, workerID)

				// Show output immediately so user sees findings in real-time
				if t != nil && result.Output != "" {
					fmt.Printf("\n  ┌─ [%s] %s\n", t.Type, t.Title)
					for _, line := range strings.Split(strings.TrimSpace(result.Output), "\n") {
						fmt.Printf("  │ %s\n", line)
					}
					fmt.Println("  └─")
				}

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

// handleTaskFailure manages retry logic for failed tasks with error classification
func (q *Queen) handleTaskFailure(ctx context.Context, taskID, workerID string, result *task.Result) {
	t, ok := q.tasks.Get(taskID)
	if !ok {
		return
	}

	errMsg := "unknown error"
	if result != nil && len(result.Errors) > 0 {
		errMsg = strings.Join(result.Errors, "; ")
	}

	// Classify the error type
	errType := errors.ClassifyError(fmt.Errorf("%s", errMsg))
	t.LastError = errMsg
	t.LastErrorType = string(errType)

	// Update error type in database
	if err := q.db.UpdateTaskErrorType(q.sessionID, taskID, t.LastErrorType); err != nil {
		q.logger.Printf("  ⚠ Warning: failed to update task error type: %v", err)
	}

	q.logger.Printf("  ❌ Task %s failed (%s): %s", taskID, errType, truncate(errMsg, 200))

	// Check if error is retryable
	isRetryable := errors.IsRetryable(fmt.Errorf("%s", errMsg))

	// Don't increment retry count for permanent errors - they won't succeed on retry
	if isRetryable && t.RetryCount < t.MaxRetries {
		t.RetryCount++

		// Calculate exponential backoff delay
		baseDelay := 2 * time.Second
		maxDelay := 60 * time.Second
		backoffDelay := errors.CalculateBackoff(baseDelay, t.RetryCount-1, maxDelay)

		q.tasks.UpdateStatus(taskID, task.StatusPending)
		q.db.UpdateTaskStatus(q.sessionID, taskID, "pending")
		q.db.UpdateTaskRetryCount(q.sessionID, taskID, t.RetryCount)

		q.logger.Printf("  🔄 Retrying task %s (attempt %d/%d) after %v backoff", taskID, t.RetryCount, t.MaxRetries, backoffDelay)

		// Apply backoff by waiting before the task becomes ready again
		// We track when the task can be retried
		go func() {
			time.Sleep(backoffDelay)
			q.store.Append("queen.retry", map[string]interface{}{
				"task_id":     taskID,
				"retry_count": t.RetryCount,
				"error":       errMsg,
				"error_type":  string(errType),
				"backoff_ms":  backoffDelay.Milliseconds(),
			})
		}()
	} else if !isRetryable {
		// Permanent error - fail immediately without wasting retries
		q.logger.Printf("  💀 Task %s has permanent error, failing immediately", taskID)
		q.tasks.UpdateStatus(taskID, task.StatusFailed)
		q.db.UpdateTaskStatus(q.sessionID, taskID, "failed")
		q.store.Append("queen.task_failed", map[string]interface{}{
			"task_id":    taskID,
			"error":      errMsg,
			"error_type": string(errType),
			"permanent":  true,
		})
	} else {
		// Max retries exceeded
		q.tasks.UpdateStatus(taskID, task.StatusFailed)
		q.db.UpdateTaskStatus(q.sessionID, taskID, "failed")
		q.store.Append("queen.task_failed", map[string]interface{}{
			"task_id":     taskID,
			"error":       errMsg,
			"error_type":  string(errType),
			"max_retries": true,
		})
	}
}

// handleFailure is the top-level failure handler
func (q *Queen) handleFailure(ctx context.Context) error {
	q.pool.KillAll()

	failed := q.tasks.Failed()

	// If there are no tasks at all, the failure happened before/during planning
	if len(q.tasks.All()) == 0 {
		errMsg := "unknown error"
		if q.lastErr != nil {
			errMsg = q.lastErr.Error()
		}
		q.store.Append("queen.failed", map[string]interface{}{
			"phase": "planning",
			"error": errMsg,
		})
		return fmt.Errorf("queen failed during planning phase: %s", errMsg)
	}

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

	b.WriteString("IMPORTANT PLANNING RULES:\n")
	b.WriteString("- Each task MUST be narrowly scoped to exactly one concern\n")
	b.WriteString("- Tasks MUST NOT make changes outside their described scope\n")
	b.WriteString("- Each task should include a \"constraints\" field listing what it must NOT do\n")
	b.WriteString("- Each task should include an \"allowed_paths\" field listing which files/dirs it may touch\n")
	b.WriteString("- Prefer more small focused tasks over fewer broad tasks\n")
	b.WriteString("- DO NOT combine unrelated changes into one task\n\n")

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
			t.ID = fmt.Sprintf("task-%d", time.Now().UnixNano())
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

// TaskResult pairs a task with its result for ordered output
type TaskResult struct {
	ID          string
	Title       string
	Type        task.Type
	Status      task.Status
	Result      *task.Result
	WorkerID    string
	CompletedAt *time.Time
}

// Results returns all task results in creation order
func (q *Queen) Results() []TaskResult {
	var results []TaskResult
	for _, t := range q.tasks.All() {
		tr := TaskResult{
			ID:          t.ID,
			Title:       t.Title,
			Type:        t.Type,
			Status:      t.Status,
			Result:      t.Result,
			WorkerID:    t.WorkerID,
			CompletedAt: t.CompletedAt,
		}
		results = append(results, tr)
	}
	return results
}

// savePhase saves the current phase and iteration to the database for resumption.
func (q *Queen) savePhase() {
	if q.sessionID != "" {
		if err := q.db.UpdateSessionPhase(q.sessionID, string(q.phase), q.iteration); err != nil {
			q.logger.Printf("⚠ Warning: failed to save session phase: %v", err)
		}
	}
}

// Close cleans up resources
func (q *Queen) Close() error {
	q.pool.KillAll()
	if q.sessionID != "" {
		// Save current phase for potential resumption
		q.savePhase()
		// Only set status to 'stopped' if current status is not terminal
		if session, err := q.db.GetSession(q.sessionID); err == nil {
			if session.Status != "done" && session.Status != "failed" {
				q.db.UpdateSessionStatus(q.sessionID, "stopped")
			}
		}
	}
	q.db.Close()
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

// printReport outputs a complete report of all task results
func (q *Queen) printReport() {
	results := q.Results()
	if len(results) == 0 {
		return
	}

	fmt.Println("")
	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║            📋 FINAL REPORT                      ║")
	fmt.Println("╚══════════════════════════════════════════════════╝")
	fmt.Printf("\n  Objective: %s\n", q.objective)

	completed := 0
	failed := 0
	for _, r := range results {
		if r.Status == task.StatusComplete {
			completed++
		} else if r.Status == task.StatusFailed {
			failed++
		}
	}
	fmt.Printf("  Tasks: %d completed, %d failed, %d total\n", completed, failed, len(results))

	for _, r := range results {
		icon := "⏳"
		switch r.Status {
		case task.StatusComplete:
			icon = "✅"
		case task.StatusFailed:
			icon = "❌"
		}

		fmt.Printf("\n  %s [%s] %s\n", icon, r.Type, r.Title)
		fmt.Println("  " + strings.Repeat("─", 48))

		if r.Result != nil && r.Result.Output != "" {
			for _, line := range strings.Split(strings.TrimSpace(r.Result.Output), "\n") {
				fmt.Printf("  %s\n", line)
			}
		} else if r.Result != nil && len(r.Result.Errors) > 0 {
			for _, e := range r.Result.Errors {
				if e != "" {
					fmt.Printf("  ERROR: %s\n", e)
				}
			}
		} else {
			fmt.Println("  (no output)")
		}
	}

	fmt.Println("")
	fmt.Println("  " + strings.Repeat("═", 48))
	fmt.Printf("  Session: %s\n", q.sessionID)
	fmt.Printf("  Log: .hive/hive.db\n")
	fmt.Println("")
}

// --- JSON parsing helpers (tolerant of LLM output variations) ---

// appendUnique appends items to a slice, skipping duplicates.
func appendUnique(slice []string, items ...string) []string {
	existing := make(map[string]bool, len(slice))
	for _, s := range slice {
		existing[s] = true
	}
	for _, item := range items {
		if !existing[item] {
			slice = append(slice, item)
			existing[item] = true
		}
	}
	return slice
}

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
			var n int
			fmt.Sscanf(val, "%d", &n)
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

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
