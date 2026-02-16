package queen

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/exedev/waggle/internal/task"
	"github.com/exedev/waggle/internal/worker"
)

// delegate assigns ready tasks to workers
func (q *Queen) delegate(ctx context.Context) error {
	q.logVerbose("📤 Delegation phase...")

	ready := q.tasks.Ready()
	if len(ready) == 0 {
		q.logVerbose("  No tasks ready (all have unmet dependencies)")
		return nil
	}

	// Sort by priority
	sort.Slice(ready, func(i, j int) bool {
		return ready[i].Priority > ready[j].Priority
	})

	for _, t := range ready {
		if q.pool.ActiveCount() >= q.cfg.Workers.MaxParallel {
			q.logVerbose("  ⏸ Max parallel workers reached, queuing remaining")
			break
		}

		adapterName := q.router.Route(t)
		if adapterName == "" {
			q.logVerbose("  ⚠ No adapter for task %s, skipping", t.ID)
			continue
		}

		// Inject default scope constraints into every task
		injectDefaultConstraints(t)

		bee, err := q.pool.Spawn(ctx, t, adapterName)
		if err != nil {
			q.logVerbose("  ⚠ Failed to spawn worker for %s: %v", t.ID, err)
			continue
		}

		q.mu.Lock()
		q.assignments[bee.ID()] = t.ID
		q.mu.Unlock()

		if err := q.tasks.UpdateStatus(t.ID, task.StatusRunning); err != nil {
			q.logger.Printf("  ⚠ Warning: failed to update task status: %v", err)
		}
		t.SetWorkerID(bee.ID())

		if err := q.db.UpdateTaskStatus(ctx, q.sessionID, t.ID, "running"); err != nil {
			q.logger.Printf("  ⚠ Warning: failed to update task status in db: %v", err)
		}
		if err := q.db.UpdateTaskWorker(ctx, q.sessionID, t.ID, bee.ID()); err != nil {
			q.logger.Printf("  ⚠ Warning: failed to update task worker in db: %v", err)
		}

		q.logVerbose("  🐝 Assigned [%s] %s -> %s (%s)", t.Type, t.Title, bee.ID(), adapterName)
	}

	return nil
}

// monitor watches running workers until all complete or fail
func (q *Queen) monitor(ctx context.Context) error {
	q.logVerbose("👁 Monitoring phase...")

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
			q.logVerbose("  ⏰ Monitoring timeout reached, killing stuck workers")
			q.pool.KillAll()
			return nil
		}

		active := q.pool.Active()
		if len(active) == 0 {
			q.logVerbose("  ✓ All workers finished")
			return nil
		}

		if pollCount%5 == 0 { // Log every 10s instead of every 2s
			q.logVerbose("  ⏳ %d workers active...", len(active))
		}
		pollCount++

		time.Sleep(pollInterval)
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
			_ = bee.Kill()
			return ctx.Err()
		case <-deadline.C:
			_ = bee.Kill()
			return fmt.Errorf("worker %s timed out after %v", bee.ID(), timeout)
		case <-ticker.C:
			status := bee.Monitor()
			if status == worker.StatusComplete || status == worker.StatusFailed {
				return nil
			}
		}
	}
}
