package queen

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HexSleeves/waggle/internal/task"
)

// TestRejectRequeueReexecute_E2E verifies the full reject→re-queue→re-execute
// cycle using the exec adapter with real shell commands. A counter file
// causes the first attempt to fail and the second to succeed.
func TestRejectRequeueReexecute_E2E(t *testing.T) {
	q := orchTestQueen(t)

	// Create a temp directory for the counter file inside the project dir
	// (orchTestQueen uses t.TempDir() as the project root).
	counterDir := filepath.Join(q.cfg.ProjectDir, "testdir")
	if err := os.MkdirAll(counterDir, 0o755); err != nil {
		t.Fatalf("mkdir counterDir: %v", err)
	}
	counterFile := filepath.Join(counterDir, "counter.txt")

	// Shell script: increment counter, fail when n < 2, succeed otherwise.
	script := fmt.Sprintf(
		`file=%s; n=0; if [ -f "$file" ]; then n=$(cat "$file"); fi; n=$((n+1)); echo "$n" > "$file"; if [ "$n" -lt 2 ]; then echo "FAIL: attempt $n" >&2; exit 1; fi; echo "SUCCESS: attempt $n"`,
		counterFile,
	)

	q.tasks.Add(&task.Task{
		ID:          "retry-task",
		Type:        task.TypeGeneric,
		Status:      task.StatusPending,
		Title:       "Retry E2E",
		Description: script,
		MaxRetries:  2,
		Timeout:     10 * time.Second,
	})

	ctx := context.Background()

	// ── First cycle: Plan → Delegate → Monitor → Review ──────────────────
	if err := q.plan(ctx); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := q.delegate(ctx); err != nil {
		t.Fatalf("delegate (1st): %v", err)
	}

	tk, _ := q.tasks.Get("retry-task")
	if tk.Status != task.StatusRunning {
		t.Fatalf("expected running after 1st delegate, got %s", tk.Status)
	}

	// Wait for the shell command to finish.
	time.Sleep(1 * time.Second)

	if err := q.monitor(ctx); err != nil {
		t.Fatalf("monitor (1st): %v", err)
	}

	done, err := q.review(ctx)
	if err != nil {
		t.Fatalf("review (1st): %v", err)
	}
	if done {
		t.Fatal("expected done=false after first (failed) attempt")
	}

	// After first failure the task should be re-queued to pending with
	// RetryCount incremented to 1.
	tk, _ = q.tasks.Get("retry-task")
	if tk.Status != task.StatusPending {
		t.Fatalf("expected pending after 1st failure, got %s", tk.Status)
	}
	if tk.GetRetryCount() != 1 {
		t.Fatalf("expected retryCount=1, got %d", tk.GetRetryCount())
	}

	// Clear the exponential-backoff timer so Ready() returns the task
	// immediately without waiting 2 s.
	tk.SetRetryAfter(time.Time{})

	// ── Second cycle: Delegate → Monitor → Review ────────────────────────
	if err := q.delegate(ctx); err != nil {
		t.Fatalf("delegate (2nd): %v", err)
	}

	tk, _ = q.tasks.Get("retry-task")
	if tk.Status != task.StatusRunning {
		t.Fatalf("expected running after 2nd delegate, got %s", tk.Status)
	}

	time.Sleep(1 * time.Second)

	if err := q.monitor(ctx); err != nil {
		t.Fatalf("monitor (2nd): %v", err)
	}

	done, err = q.review(ctx)
	if err != nil {
		t.Fatalf("review (2nd): %v", err)
	}
	if !done {
		t.Fatal("expected done=true after second (successful) attempt")
	}

	tk, _ = q.tasks.Get("retry-task")
	if tk.Status != task.StatusComplete {
		t.Fatalf("expected complete, got %s", tk.Status)
	}
	if tk.GetRetryCount() != 1 {
		t.Fatalf("expected retryCount=1 (unchanged after success), got %d", tk.GetRetryCount())
	}
}

// TestRejectExhaustsRetries_E2E verifies that a task which always fails is
// permanently marked failed once MaxRetries is exhausted.
func TestRejectExhaustsRetries_E2E(t *testing.T) {
	q := orchTestQueen(t)

	q.tasks.Add(&task.Task{
		ID:          "always-fail",
		Type:        task.TypeGeneric,
		Status:      task.StatusPending,
		Title:       "Always Fail",
		Description: "exit 1",
		MaxRetries:  1,
		Timeout:     10 * time.Second,
	})

	ctx := context.Background()

	// ── First attempt ────────────────────────────────────────────────────
	if err := q.plan(ctx); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := q.delegate(ctx); err != nil {
		t.Fatalf("delegate (1st): %v", err)
	}

	time.Sleep(1 * time.Second)

	if err := q.monitor(ctx); err != nil {
		t.Fatalf("monitor (1st): %v", err)
	}

	_, err := q.review(ctx)
	if err != nil {
		t.Fatalf("review (1st): %v", err)
	}

	tk, _ := q.tasks.Get("always-fail")
	if tk.Status != task.StatusPending {
		t.Fatalf("expected pending after 1st failure (retry available), got %s", tk.Status)
	}
	if tk.GetRetryCount() != 1 {
		t.Fatalf("expected retryCount=1, got %d", tk.GetRetryCount())
	}

	// Clear backoff so the second attempt can proceed immediately.
	tk.SetRetryAfter(time.Time{})

	// ── Second attempt (exhausts retries) ────────────────────────────────
	if err := q.delegate(ctx); err != nil {
		t.Fatalf("delegate (2nd): %v", err)
	}

	time.Sleep(1 * time.Second)

	if err := q.monitor(ctx); err != nil {
		t.Fatalf("monitor (2nd): %v", err)
	}

	_, err = q.review(ctx)
	// review may return an error ("N tasks failed") when all hope is lost.
	// That is acceptable — what matters is the final task state.
	if err != nil {
		t.Logf("review (2nd) returned (expected) error: %v", err)
	}

	tk, _ = q.tasks.Get("always-fail")
	if tk.Status != task.StatusFailed {
		t.Fatalf("expected failed after exhausting retries, got %s", tk.Status)
	}
	if tk.GetRetryCount() != tk.MaxRetries {
		t.Fatalf("expected retryCount=%d (==MaxRetries), got %d", tk.MaxRetries, tk.GetRetryCount())
	}
}
