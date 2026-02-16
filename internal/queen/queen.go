package queen

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/exedev/waggle/internal/adapter"
	"github.com/exedev/waggle/internal/blackboard"
	"github.com/exedev/waggle/internal/bus"
	"github.com/exedev/waggle/internal/compact"
	"github.com/exedev/waggle/internal/config"
	"github.com/exedev/waggle/internal/llm"
	"github.com/exedev/waggle/internal/safety"
	"github.com/exedev/waggle/internal/state"
	"github.com/exedev/waggle/internal/task"
	"github.com/exedev/waggle/internal/worker"
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
	mu        sync.RWMutex
	closeOnce sync.Once

	cfg      *config.Config
	bus      *bus.MessageBus
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

	llm   llm.Client    // LLM client for AI-backed review/replan (nil = disabled)
	guard *safety.Guard // shared safety guard for tool calls

	suppressReport bool // TUI mode: don't print report to stdout
	quiet          bool // Quiet mode: only print completions/failures
	suppressBanner bool // JSON mode: don't print header banner

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
func (q *Queen) ResumeSession(ctx context.Context, sessionID string) (string, error) {
	// Get session info including phase and iteration
	sessionInfo, err := q.db.GetSessionFull(ctx, sessionID)
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
	if err := q.db.ResetRunningTasks(ctx, sessionID); err != nil {
		q.logger.Printf("⚠ Warning: failed to reset running tasks: %v", err)
	}

	// Load all tasks from the session
	taskRows, err := q.db.GetTasks(ctx, sessionID)
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

	// Initialize SQLite DB
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
	maxOut := cfg.Workers.MaxOutputSize
	registry := adapter.NewRegistry()
	registry.Register(adapter.NewClaudeAdapter(
		cfg.Adapters["claude-code"].Command,
		cfg.Adapters["claude-code"].Args,
		cfg.ProjectDir,
		guard,
	).WithMaxOutput(maxOut))
	registry.Register(adapter.NewCodexAdapter(
		cfg.Adapters["codex"].Command,
		cfg.Adapters["codex"].Args,
		cfg.ProjectDir,
		guard,
	).WithMaxOutput(maxOut))
	registry.Register(adapter.NewOpenCodeAdapter(
		cfg.Adapters["opencode"].Command,
		cfg.Adapters["opencode"].Args,
		cfg.ProjectDir,
		guard,
	).WithMaxOutput(maxOut))

	// Register exec adapter (always available fallback)
	registry.Register(adapter.NewExecAdapter(cfg.ProjectDir, guard).WithMaxOutput(maxOut))

	// Register kimi adapter
	registry.Register(adapter.NewKimiAdapter(
		cfg.Adapters["kimi"].Command,
		cfg.Adapters["kimi"].Args,
		cfg.ProjectDir,
		guard,
	).WithMaxOutput(maxOut))

	// Register gemini adapter
	registry.Register(adapter.NewGeminiAdapter(
		cfg.Adapters["gemini"].Command,
		cfg.Adapters["gemini"].Args,
		cfg.ProjectDir,
		guard,
	).WithMaxOutput(maxOut))

	router := adapter.NewTaskRouter(registry, cfg.Workers.DefaultAdapter)

	// Initialize worker pool
	pool := worker.NewPool(
		cfg.Workers.MaxParallel,
		registry.WorkerFactory(),
		msgBus,
	)

	// Context management
	ctxMgr := compact.NewContext(200000) // ~200k tokens

	// Initialize LLM client for Queen's own reasoning (review, replan)
	var llmClient llm.Client
	if cfg.Queen.Provider != "" {
		var err error
		llmClient, err = llm.NewFromConfig(llm.ProviderConfig{
			Provider: cfg.Queen.Provider,
			Model:    cfg.Queen.Model,
			APIKey:   cfg.Queen.APIKey,
			BaseURL:  cfg.Queen.BaseURL,
			WorkDir:  cfg.ProjectDir,
		})
		if err != nil {
			logger.Printf("⚠ Queen LLM init failed: %v (review/replan disabled)", err)
			llmClient = nil
		} else {
			logger.Printf("✓ Queen LLM enabled (%s) for review/replan", cfg.Queen.Provider)
		}
	}

	q := &Queen{
		cfg:         cfg,
		bus:         msgBus,
		db:          db,
		board:       board,
		tasks:       tasks,
		pool:        pool,
		router:      router,
		registry:    registry,
		ctx:         ctxMgr,
		llm:         llmClient,
		guard:       guard,
		phase:       PhasePlan,
		logger:      logger,
		assignments: make(map[string]string),
	}

	// Wire up event logging to SQLite
	msgBus.SubscribeAll(func(msg bus.Message) {
		if sid := q.sessionID; sid != "" {
			_, err = q.db.AppendEvent(context.Background(), sid, string(msg.Type), msg)
			if err != nil {
				q.logger.Printf("⚠ Warning: failed to append event %s: %v", msg.Type, err)
			}
		}
	})

	return q, nil
}

// setupAdapters verifies adapter availability, routes all task types to the
// chosen (or fallback) adapter, runs a health check, and logs the result.
// Both Run() and RunAgent() call this during preflight.
func (q *Queen) setupAdapters(ctx context.Context) error {
	available := q.registry.Available()
	if len(available) == 0 {
		return fmt.Errorf("no adapters available — install claude, codex, or opencode CLI")
	}
	defaultAdapter := q.cfg.Workers.DefaultAdapter
	allTypes := []task.Type{task.TypeCode, task.TypeResearch, task.TypeTest, task.TypeReview, task.TypeGeneric}
	if a, ok := q.registry.Get(defaultAdapter); !ok || !a.Available() {
		if !q.quiet {
			q.logger.Printf("⚠ Default adapter %q not available, falling back to: %s", defaultAdapter, available[0])
		}
		for _, tt := range allTypes {
			q.router.SetRoute(tt, available[0])
		}
	} else {
		// Ensure all task types route to the user's chosen adapter
		for _, tt := range allTypes {
			q.router.SetRoute(tt, defaultAdapter)
		}
	}
	// Show available adapters (exclude 'exec' unless it's the default)
	displayAdapters := make([]string, 0, len(available))
	for _, name := range available {
		if name != "exec" || defaultAdapter == "exec" {
			displayAdapters = append(displayAdapters, name)
		}
	}
	if !q.quiet {
		q.logger.Printf("✓ Using adapter: %s | Available: %v", defaultAdapter, displayAdapters)
	}

	// Health check: verify the adapter actually works
	if adapter, ok := q.registry.Get(defaultAdapter); ok {
		if err := adapter.HealthCheck(ctx); err != nil {
			return fmt.Errorf("adapter health check failed: %w", err)
		}
		if !q.quiet {
			q.logger.Printf("✓ Adapter health check passed")
		}
	}

	return nil
}

// Run executes the Queen's main loop: Plan -> Delegate -> Monitor -> Review
func (q *Queen) Run(ctx context.Context, objective string) error {
	q.objective = objective
	q.logger.Printf("🐝 Waggle starting | Objective: %s", objective)

	// Preflight: verify and configure adapters
	if err := q.setupAdapters(ctx); err != nil {
		return err
	}

	// Create DB session
	q.sessionID = fmt.Sprintf("session-%d", time.Now().UnixNano())
	if err := q.db.CreateSession(ctx, q.sessionID, objective); err != nil {
		q.logger.Printf("⚠ DB: failed to create session: %v", err)
	}

	for q.iteration = 0; q.iteration < q.cfg.Queen.MaxIterations; q.iteration++ {
		select {
		case <-ctx.Done():
			q.logger.Println("⛔ Context cancelled, shutting down")
			q.pool.KillAll()
			return ctx.Err()
		default:
		}

		q.logVerbose("\n━━━ Iteration %d | Phase: %s ━━━", q.iteration+1, q.phase)

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
			} else if len(q.tasks.Ready()) > 0 {
				// Tasks are already unblocked — skip planning, go straight to delegation
				q.phase = PhaseDelegate
			} else {
				q.phase = PhasePlan // Need LLM to create more tasks
			}
			q.savePhase()

		case PhaseDone:
			q.logger.Println("✅ All tasks complete!")
			if err := q.db.UpdateSessionStatus(ctx, q.sessionID, "done"); err != nil {
				q.logger.Printf("⚠ Warning: failed to update session status: %v", err)
			}
			q.printReport()
			return nil

		case PhaseFailed:
			return q.handleFailure(ctx)
		}

		// Compact context if needed
		if q.ctx.NeedsCompaction() {
			q.logVerbose("📦 Compacting context...")
			err := q.ctx.Compact(compact.DefaultSummarizer)
			if err != nil {
				q.logger.Printf("⚠ Warning: failed to compact context: %v", err)
			}
		}
	}

	if err := q.db.UpdateSessionStatus(ctx, q.sessionID, "failed"); err != nil {
		q.logger.Printf("⚠ Warning: failed to update session status: %v", err)
	}
	return fmt.Errorf("max iterations (%d) reached", q.cfg.Queen.MaxIterations)
}

// review examines completed work, handles failures
func (q *Queen) review(ctx context.Context) (bool, error) {
	q.logVerbose("🔍 Review phase...")

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
			// Worker gone — prune stale assignment
			q.mu.Lock()
			delete(q.assignments, workerID)
			q.mu.Unlock()
			continue
		}

		switch bee.Monitor() {
		case worker.StatusComplete:
			result := bee.Result()
			if result != nil && result.Success {
				if err := q.tasks.UpdateStatus(taskID, task.StatusComplete); err != nil {
					q.logger.Printf("⚠ Warning: failed to update task status: %v", err)
				}
				t, _ := q.tasks.Get(taskID)
				if t != nil {
					t.SetResult(result)
				}

				if err := q.db.UpdateTaskStatus(ctx, q.sessionID, taskID, "complete"); err != nil {
					q.logger.Printf("⚠ Warning: failed to update task status: %v", err)
				}
				err := q.db.UpdateTaskResult(ctx, q.sessionID, taskID, result)
				if err != nil {
					q.logger.Printf("⚠ Warning: failed to update task result %s: %v", taskID, err)
				}

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
				err = q.db.PostBlackboard(ctx, q.sessionID, bbKey, result.Output, workerID, taskID, tagsStr)
				if err != nil {
					q.logger.Printf("⚠ Warning: failed to post blackboard entry %s: %v", bbKey, err)
				}

				q.logVerbose("  ✅ Task %s completed by %s", taskID, workerID)

				// LLM review: evaluate output quality if configured
				if q.llm != nil && t != nil {
					verdict, err := q.reviewWithLLM(ctx, taskID, t, result)
					if err != nil {
						q.logVerbose("  ⚠ LLM review failed: %v (accepting result)", err)
					} else if !verdict.Approved {
						q.logVerbose("  🔙 LLM rejected task %s: %s", taskID, verdict.Reason)
						if len(verdict.Suggestions) > 0 {
							for _, s := range verdict.Suggestions {
								q.logVerbose("     💡 %s", s)
							}
						}
						// Re-queue with suggestions appended to description
						if t.GetRetryCount() < t.MaxRetries {
							newCount := t.IncrRetryCount()
							rejectionMsg := "\n\nPREVIOUS ATTEMPT REJECTED: " + verdict.Reason
							if len(verdict.Suggestions) > 0 {
								rejectionMsg += "\nSuggestions: " + strings.Join(verdict.Suggestions, "; ")
							}
							t.AppendDescription(rejectionMsg)
							_ = newCount // used for logging below
							if err := q.tasks.UpdateStatus(taskID, task.StatusPending); err != nil {
								q.logger.Printf("⚠ Warning: failed to update task status: %v", err)
							}
							if err := q.db.UpdateTaskStatus(ctx, q.sessionID, taskID, "pending"); err != nil {
								q.logger.Printf("⚠ Warning: failed to update task status: %v", err)
							}
							q.logVerbose("  🔄 Re-queued task %s (attempt %d/%d)", taskID, newCount, t.MaxRetries)
							q.mu.Lock()
							delete(q.assignments, workerID)
							q.mu.Unlock()
							continue
						}
						q.logVerbose("  💀 Task %s rejected but max retries reached, accepting", taskID)
					} else {
						q.logVerbose("  ✓ LLM approved task %s", taskID)
					}
				}

				// Show output immediately so user sees findings in real-time (unless suppressed)
				if t != nil && result.Output != "" && !q.suppressReport {
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
			// Prune completed assignment
			q.mu.Lock()
			delete(q.assignments, workerID)
			q.mu.Unlock()

		case worker.StatusFailed:
			result := bee.Result()
			q.handleTaskFailure(ctx, taskID, workerID, result)
			// Prune completed assignment
			q.mu.Lock()
			delete(q.assignments, workerID)
			q.mu.Unlock()
		}
	}

	// Clean up finished workers
	q.pool.Cleanup()

	// Check if all tasks are done
	if q.tasks.AllComplete() {
		// LLM replan: check if more work is needed
		if q.llm != nil {
			newTasks, err := q.replanWithLLM(ctx)
			if err != nil {
				q.logVerbose("  ⚠ LLM replan failed: %v (finishing)", err)
			} else if len(newTasks) > 0 {
				for _, t := range newTasks {
					q.persistNewTask(ctx, t)
					q.logVerbose("  📌 New task from replan: [%s] %s", t.Type, t.Title)
				}
				return false, nil // More work to do
			}
		}
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
			q.logVerbose("  💀 %d tasks failed with no recovery possible", len(failed))
			return false, fmt.Errorf("%d tasks failed", len(failed))
		}
	}

	return false, nil
}

// --- Shared helpers ---

// injectDefaultConstraints adds scope-limiting constraints to a task.
// Used by both delegate() and agent-mode handleAssignTask().
func injectDefaultConstraints(t *task.Task) {
	existing := t.GetConstraints()
	t.SetConstraints(appendUnique(existing,
		"Do NOT make changes outside the scope described in this task",
		"Do NOT refactor, reorganize, or 'improve' code unrelated to this task",
		"Do NOT modify function signatures unless explicitly asked to",
		"If you find issues outside your scope, note them in your output but do NOT fix them",
	))
}

// persistNewTask adds a task to the graph and persists it to the database.
func (q *Queen) persistNewTask(ctx context.Context, t *task.Task) {
	q.tasks.Add(t)
	err := q.db.InsertTask(ctx, q.sessionID, state.TaskRow{
		ID: t.ID, Type: string(t.Type), Status: string(t.Status),
		Priority: int(t.Priority), Title: t.Title, Description: t.Description,
		MaxRetries: t.MaxRetries, DependsOn: strings.Join(t.DependsOn, ","),
	})
	if err != nil {
		q.logger.Printf("⚠ Warning: failed to persist new task %s: %v", t.ID, err)
		q.tasks.Remove(t.ID)
	}
}

// savePhase saves the current phase and iteration to the database for resumption.
func (q *Queen) savePhase() {
	if q.sessionID != "" {
		if err := q.db.UpdateSessionPhase(context.Background(), q.sessionID, string(q.phase), q.iteration); err != nil {
			q.logger.Printf("⚠ Warning: failed to save session phase: %v", err)
		}
	}
}

// Close cleans up resources
func (q *Queen) Close() error {
	var closeErr error
	q.closeOnce.Do(func() {
		q.pool.KillAll()
		if q.sessionID != "" {
			q.savePhase()
			if session, err := q.db.GetSession(context.Background(), q.sessionID); err == nil {
				if session.Status != "done" && session.Status != "failed" {
					if err := q.db.UpdateSessionStatus(context.Background(), q.sessionID, "stopped"); err != nil {
						q.logger.Printf("⚠ Warning: failed to update session status: %v", err)
					}
				}
			}
		}
		closeErr = q.db.Close()
	})
	return closeErr
}

// SetLogger replaces the Queen's logger (used by TUI to capture output).
func (q *Queen) SetLogger(logger *log.Logger) {
	q.logger = logger
}

// SuppressReport prevents printReport from writing to stdout (for TUI mode).
func (q *Queen) SuppressReport() {
	q.suppressReport = true
}

// SetQuiet enables quiet mode where only task completions/failures are shown.
func (q *Queen) SetQuiet(quiet bool) {
	q.quiet = quiet
}

// SetSuppressBanner prevents printing the header banner (for JSON mode).
func (q *Queen) SetSuppressBanner(suppress bool) {
	q.suppressBanner = suppress
}

// SuppressBanner returns true if banner suppression is enabled.
func (q *Queen) SuppressBanner() bool {
	return q.suppressBanner
}

// logVerbose logs a message only if not in quiet mode.
func (q *Queen) logVerbose(format string, v ...interface{}) {
	if !q.quiet {
		q.logger.Printf(format, v...)
	}
}

// Bus returns the Queen's message bus (used by TUI to subscribe to events).
func (q *Queen) Bus() *bus.MessageBus {
	return q.bus
}

// ActiveWorkerOutputs returns the current output of all workers in the pool.
// Returns a map of workerID -> output string.
func (q *Queen) ActiveWorkerOutputs() map[string]string {
	results := make(map[string]string)
	q.mu.RLock()
	for workerID := range q.assignments {
		if bee, ok := q.pool.Get(workerID); ok {
			results[workerID] = bee.Output()
		}
	}
	q.mu.RUnlock()
	return results
}

// Status returns the current queen status for display
func (q *Queen) Status() map[string]interface{} {
	q.mu.RLock()
	defer q.mu.RUnlock()

	return map[string]interface{}{
		"session_id":     q.sessionID,
		"phase":          q.phase,
		"iteration":      q.iteration,
		"objective":      q.objective,
		"active_workers": q.pool.ActiveCount(),
		"total_tasks":    len(q.tasks.All()),
		"ready_tasks":    len(q.tasks.Ready()),
		"context_tokens": q.ctx.TokenCount(),
	}
}
