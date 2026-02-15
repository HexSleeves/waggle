package queen

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/exedev/waggle/internal/adapter"
	"github.com/exedev/waggle/internal/blackboard"
	"github.com/exedev/waggle/internal/bus"
	"github.com/exedev/waggle/internal/compact"
	"github.com/exedev/waggle/internal/config"
	"github.com/exedev/waggle/internal/safety"
	"github.com/exedev/waggle/internal/state"
	"github.com/exedev/waggle/internal/task"
	"github.com/exedev/waggle/internal/worker"
)

// orchTestQueen builds a Queen wired with the exec adapter so workers
// actually run shell commands. Pre-defined tasks skip the planning phase.
func orchTestQueen(t *testing.T) *Queen {
	t.Helper()
	tmpDir := t.TempDir()
	hiveDir := filepath.Join(tmpDir, ".hive")
	if err := os.MkdirAll(hiveDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	db, err := state.OpenDB(hiveDir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	msgBus := bus.New(100)

	cfg := &config.Config{
		ProjectDir: tmpDir,
		HiveDir:    ".hive",
		Queen: config.QueenConfig{
			MaxIterations: 20,
		},
		Workers: config.WorkerConfig{
			MaxParallel:    4,
			MaxRetries:     1,
			DefaultTimeout: 30 * time.Second,
			DefaultAdapter: "exec",
		},
		Safety: config.SafetyConfig{
			AllowedPaths: []string{"."},
			MaxFileSize:  10 * 1024 * 1024,
		},
		Adapters: map[string]config.AdapterConfig{},
	}

	guard, err := safety.NewGuard(cfg.Safety, cfg.ProjectDir)
	if err != nil {
		t.Fatalf("guard: %v", err)
	}

	registry := adapter.NewRegistry()
	registry.Register(adapter.NewExecAdapter(tmpDir, guard))
	router := adapter.NewTaskRouter(registry, "exec")
	pool := worker.NewPool(cfg.Workers.MaxParallel, registry.WorkerFactory(), msgBus)

	logger := log.New(os.Stderr, "[ORCH-TEST] ", log.LstdFlags)

	q := &Queen{
		cfg:            cfg,
		bus:            msgBus,
		db:             db,
		board:          blackboard.New(msgBus),
		tasks:          task.NewTaskGraph(msgBus),
		pool:           pool,
		router:         router,
		registry:       registry,
		ctx:            compact.NewContext(100000),
		guard:          guard,
		phase:          PhasePlan,
		logger:         logger,
		sessionID:      "orch-test",
		assignments:    make(map[string]string),
		suppressReport: true,
	}

	if err := db.CreateSession(context.Background(), q.sessionID, "test"); err != nil {
		t.Fatalf("create session: %v", err)
	}

	return q
}

func TestDelegateAssignsReadyTasks(t *testing.T) {
	q := orchTestQueen(t)

	// Add two independent tasks
	q.tasks.Add(&task.Task{
		ID: "t1", Type: task.TypeGeneric, Status: task.StatusPending,
		Title: "Task 1", Description: "echo hello",
		Timeout: 10 * time.Second,
	})
	q.tasks.Add(&task.Task{
		ID: "t2", Type: task.TypeGeneric, Status: task.StatusPending,
		Title: "Task 2", Description: "echo world",
		Timeout: 10 * time.Second,
	})

	if err := q.delegate(context.Background()); err != nil {
		t.Fatalf("delegate: %v", err)
	}

	// Both should now be running
	t1, _ := q.tasks.Get("t1")
	t2, _ := q.tasks.Get("t2")
	if t1.Status != task.StatusRunning {
		t.Errorf("t1 expected running, got %s", t1.Status)
	}
	if t2.Status != task.StatusRunning {
		t.Errorf("t2 expected running, got %s", t2.Status)
	}

	// Should have 2 assignments
	if len(q.assignments) != 2 {
		t.Errorf("expected 2 assignments, got %d", len(q.assignments))
	}
}

func TestDelegateRespectsMaxParallel(t *testing.T) {
	q := orchTestQueen(t)
	q.cfg.Workers.MaxParallel = 1
	// Recreate pool with limit of 1
	q.pool = worker.NewPool(1, q.registry.WorkerFactory(), q.bus)

	q.tasks.Add(&task.Task{
		ID: "t1", Type: task.TypeGeneric, Status: task.StatusPending,
		Title: "Task 1", Description: "echo a",
		Timeout: 10 * time.Second,
	})
	q.tasks.Add(&task.Task{
		ID: "t2", Type: task.TypeGeneric, Status: task.StatusPending,
		Title: "Task 2", Description: "echo b",
		Timeout: 10 * time.Second,
	})

	if err := q.delegate(context.Background()); err != nil {
		t.Fatalf("delegate: %v", err)
	}

	// Only 1 should be running
	running := 0
	for _, tk := range q.tasks.All() {
		if tk.Status == task.StatusRunning {
			running++
		}
	}
	if running != 1 {
		t.Errorf("expected 1 running, got %d", running)
	}
}

func TestDelegateRespectsDependencies(t *testing.T) {
	q := orchTestQueen(t)

	q.tasks.Add(&task.Task{
		ID: "t1", Type: task.TypeGeneric, Status: task.StatusPending,
		Title: "First", Description: "echo first",
		Timeout: 10 * time.Second,
	})
	q.tasks.Add(&task.Task{
		ID: "t2", Type: task.TypeGeneric, Status: task.StatusPending,
		Title: "Second", Description: "echo second",
		DependsOn: []string{"t1"},
		Timeout:   10 * time.Second,
	})

	if err := q.delegate(context.Background()); err != nil {
		t.Fatalf("delegate: %v", err)
	}

	// t1 running, t2 still pending (dep not met)
	t1, _ := q.tasks.Get("t1")
	t2, _ := q.tasks.Get("t2")
	if t1.Status != task.StatusRunning {
		t.Errorf("t1 expected running, got %s", t1.Status)
	}
	if t2.Status != task.StatusPending {
		t.Errorf("t2 expected pending, got %s", t2.Status)
	}
}

func TestMonitorDetectsCompletion(t *testing.T) {
	q := orchTestQueen(t)

	// Add a fast task and delegate it
	q.tasks.Add(&task.Task{
		ID: "t1", Type: task.TypeGeneric, Status: task.StatusPending,
		Title: "Fast", Description: "echo done",
		Timeout: 10 * time.Second,
	})

	if err := q.delegate(context.Background()); err != nil {
		t.Fatalf("delegate: %v", err)
	}

	// Wait for the worker to finish
	time.Sleep(500 * time.Millisecond)

	// Monitor should detect completion
	if err := q.monitor(context.Background()); err != nil {
		t.Fatalf("monitor: %v", err)
	}

	// After review, task should be complete
	done, err := q.review(context.Background())
	if err != nil {
		t.Fatalf("review: %v", err)
	}

	t1, _ := q.tasks.Get("t1")
	if t1.Status != task.StatusComplete {
		t.Errorf("expected complete, got %s", t1.Status)
	}
	if !done {
		t.Error("expected done=true since all tasks complete")
	}
}

func TestReviewApprovesSuccessfulTask(t *testing.T) {
	q := orchTestQueen(t)

	q.tasks.Add(&task.Task{
		ID: "t1", Type: task.TypeGeneric, Status: task.StatusPending,
		Title: "OK task", Description: "echo success",
		Timeout: 10 * time.Second,
	})

	// Delegate, wait, monitor
	q.delegate(context.Background())
	time.Sleep(500 * time.Millisecond)
	q.monitor(context.Background())

	// Review should mark it complete
	done, err := q.review(context.Background())
	if err != nil {
		t.Fatalf("review: %v", err)
	}

	t1, _ := q.tasks.Get("t1")
	if t1.Status != task.StatusComplete {
		t.Errorf("expected complete, got %s", t1.Status)
	}
	if t1.Result == nil {
		t.Error("expected result to be set")
	} else if !t1.Result.Success {
		t.Error("expected result.Success=true")
	}
	if !done {
		t.Error("expected done=true")
	}
}

func TestReviewHandlesFailedTask(t *testing.T) {
	q := orchTestQueen(t)

	q.tasks.Add(&task.Task{
		ID: "t1", Type: task.TypeGeneric, Status: task.StatusPending,
		Title: "Failing", Description: "exit 1",
		MaxRetries: 1,
		Timeout:    10 * time.Second,
	})

	q.delegate(context.Background())
	time.Sleep(500 * time.Millisecond)
	q.monitor(context.Background())

	// Review should handle the failure
	done, err := q.review(context.Background())
	if err != nil {
		t.Fatalf("review: %v", err)
	}

	// Should not be done — failed tasks remain
	if done {
		t.Error("expected done=false with failed task")
	}
}

func TestFullLoopWithPreDefinedTasks(t *testing.T) {
	q := orchTestQueen(t)
	q.suppressReport = true

	// Pre-define two independent tasks
	q.SetTasks([]*task.Task{
		{
			ID: "write", Type: task.TypeCode, Status: task.StatusPending,
			Priority: task.PriorityHigh,
			Title: "Write file", Description: "echo hello > /dev/null",
			MaxRetries: 1, Timeout: 10 * time.Second,
		},
		{
			ID: "verify", Type: task.TypeTest, Status: task.StatusPending,
			Priority: task.PriorityNormal,
			Title: "Verify", Description: "echo verified",
			DependsOn:  []string{"write"},
			MaxRetries: 1, Timeout: 10 * time.Second,
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := q.Run(ctx, "Test the full loop")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Both tasks should be complete
	for _, id := range []string{"write", "verify"} {
		tk, ok := q.tasks.Get(id)
		if !ok {
			t.Errorf("task %s not found", id)
			continue
		}
		if tk.Status != task.StatusComplete {
			t.Errorf("task %s expected complete, got %s", id, tk.Status)
		}
	}
}

func TestFullLoopParallelTasks(t *testing.T) {
	q := orchTestQueen(t)
	q.suppressReport = true

	// Three independent tasks — should all run in parallel
	q.SetTasks([]*task.Task{
		{ID: "a", Type: task.TypeGeneric, Status: task.StatusPending, Title: "A", Description: "echo a", Timeout: 10 * time.Second},
		{ID: "b", Type: task.TypeGeneric, Status: task.StatusPending, Title: "B", Description: "echo b", Timeout: 10 * time.Second},
		{ID: "c", Type: task.TypeGeneric, Status: task.StatusPending, Title: "C", Description: "echo c", Timeout: 10 * time.Second},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := q.Run(ctx, "Parallel test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, id := range []string{"a", "b", "c"} {
		tk, _ := q.tasks.Get(id)
		if tk.Status != task.StatusComplete {
			t.Errorf("task %s: expected complete, got %s", id, tk.Status)
		}
	}
}

func TestPhaseTransitions(t *testing.T) {
	q := orchTestQueen(t)

	// Start in PhasePlan
	if q.phase != PhasePlan {
		t.Errorf("expected PhasePlan, got %s", q.phase)
	}

	// After plan with pre-defined tasks, should move to delegate
	q.SetTasks([]*task.Task{
		{ID: "t1", Type: task.TypeGeneric, Status: task.StatusPending,
			Title: "T", Description: "echo t", Timeout: 10 * time.Second},
	})

	if err := q.plan(context.Background()); err != nil {
		t.Fatalf("plan: %v", err)
	}

	// Tasks should be loaded
	if len(q.tasks.All()) != 1 {
		t.Errorf("expected 1 task, got %d", len(q.tasks.All()))
	}
}
