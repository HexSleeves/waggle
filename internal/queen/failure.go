package queen

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/HexSleeves/waggle/internal/errors"
	"github.com/HexSleeves/waggle/internal/task"
)

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
	t.SetLastError(errMsg, string(errType))

	// Update error type in database
	if err := q.db.UpdateTaskErrorType(ctx, q.sessionID, taskID, string(errType)); err != nil {
		q.logger.Printf("  ⚠ Warning: failed to update task error type: %v", err)
	}

	q.Printer().Error("Task %s failed (%s): %s", taskID, errType, truncate(errMsg, 200))

	// Check if error is retryable
	isRetryable := errors.IsRetryable(fmt.Errorf("%s", errMsg))

	// Don't increment retry count for permanent errors - they won't succeed on retry
	if isRetryable && t.GetRetryCount() < t.MaxRetries {
		newCount := t.IncrRetryCount()

		// Calculate exponential backoff delay
		baseDelay := 2 * time.Second
		maxDelay := 60 * time.Second
		backoffDelay := errors.CalculateBackoff(baseDelay, newCount-1, maxDelay)

		if err := q.tasks.UpdateStatus(taskID, task.StatusPending); err != nil {
			q.logger.Printf("  ⚠ Warning: failed to update task status: %v", err)
		}
		if err := q.db.UpdateTaskStatus(ctx, q.sessionID, taskID, "pending"); err != nil {
			q.logger.Printf("  ⚠ Warning: failed to update task status in db: %v", err)
		}
		if err := q.db.UpdateTaskRetryCount(ctx, q.sessionID, taskID, newCount); err != nil {
			q.logger.Printf("  ⚠ Warning: failed to update task retry count in db: %v", err)
		}

		// Apply backoff: set RetryAfter so Ready() skips this task until the delay elapses
		t.SetRetryAfter(time.Now().Add(backoffDelay))

		q.Printer().Info("Retrying task %s (attempt %d/%d) after %v backoff", taskID, newCount, t.MaxRetries, backoffDelay)
	} else if !isRetryable {
		// Permanent error - fail immediately without wasting retries
		q.Printer().Error("Task %s has permanent error, failing immediately", taskID)
		if err := q.tasks.UpdateStatus(taskID, task.StatusFailed); err != nil {
			q.logger.Printf("  ⚠ Warning: failed to update task status: %v", err)
		}
		if err := q.db.UpdateTaskStatus(ctx, q.sessionID, taskID, "failed"); err != nil {
			q.logger.Printf("  ⚠ Warning: failed to update task status in db: %v", err)
		}
	} else {
		// Max retries exceeded
		if err := q.tasks.UpdateStatus(taskID, task.StatusFailed); err != nil {
			q.logger.Printf("  ⚠ Warning: failed to update task status: %v", err)
		}
		if err := q.db.UpdateTaskStatus(ctx, q.sessionID, taskID, "failed"); err != nil {
			q.logger.Printf("  ⚠ Warning: failed to update task status in db: %v", err)
		}
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
		return fmt.Errorf("queen failed during planning phase: %s", errMsg)
	}

	var errs []string
	for _, t := range failed {
		errs = append(errs, fmt.Sprintf("[%s] %s", t.ID, t.Title))
	}

	return fmt.Errorf("queen failed: %d tasks could not be completed: %s",
		len(failed), strings.Join(errs, ", "))
}
