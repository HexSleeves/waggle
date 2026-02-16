package queen

import (
	"context"
	"fmt"
	"strings"

	"github.com/exedev/waggle/internal/task"
)

const replanSystemPrompt = `You are a re-planning agent in a multi-agent orchestration system.
Your job is to decide whether additional tasks are needed based on work completed so far.
You must output ONLY a JSON array — either empty [] or containing new task objects.
Do NOT include tasks that already exist. Do NOT repeat completed or pending work.`

// replanWithLLM asks the LLM whether additional tasks are needed given
// the current state of execution and returns any new tasks to add.
func (q *Queen) replanWithLLM(ctx context.Context) ([]*task.Task, error) {
	prompt := q.buildReplanPrompt()

	q.logger.Println("🔄 Re-planning: consulting LLM for additional tasks...")

	response, err := q.llm.Chat(ctx, replanSystemPrompt, prompt)
	if err != nil {
		return nil, fmt.Errorf("replan LLM call: %w", err)
	}

	response = strings.TrimSpace(response)

	// Fast path: empty array means no new tasks
	if response == "[]" {
		q.logger.Println("  ✓ No additional tasks needed")
		return nil, nil
	}

	tasks, err := q.parsePlanOutput(response)
	if err != nil {
		return nil, fmt.Errorf("replan parse: %w", err)
	}

	// Filter out any tasks whose IDs collide with existing ones
	existing := make(map[string]bool)
	for _, t := range q.tasks.All() {
		existing[t.ID] = true
	}
	var newTasks []*task.Task
	for _, t := range tasks {
		if existing[t.ID] {
			q.logger.Printf("  ⚠ Skipping duplicate task ID: %s", t.ID)
			continue
		}
		newTasks = append(newTasks, t)
	}

	q.logger.Printf("  📋 Re-plan produced %d new task(s)", len(newTasks))
	return newTasks, nil
}

// buildReplanPrompt constructs the user prompt for re-planning, including
// the objective, completed work, failures, and pending tasks.
func (q *Queen) buildReplanPrompt() string {
	var b strings.Builder

	// Objective
	b.WriteString(fmt.Sprintf("OBJECTIVE: %s\n\n", q.objective))

	// Completed tasks with outputs
	allTasks := q.tasks.All()

	var completed, failed, pending []*task.Task
	for _, t := range allTasks {
		switch t.GetStatus() {
		case task.StatusComplete:
			completed = append(completed, t)
		case task.StatusFailed:
			failed = append(failed, t)
		case task.StatusPending, task.StatusAssigned:
			pending = append(pending, t)
		}
	}

	b.WriteString("COMPLETED TASKS:\n")
	if len(completed) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, t := range completed {
		b.WriteString(fmt.Sprintf("- [%s] %s (id: %s)\n", t.Type, t.Title, t.ID))
		output := q.taskOutput(t)
		if output != "" {
			b.WriteString(fmt.Sprintf("  Output: %s\n", truncate(output, 500)))
		}
	}

	b.WriteString("\nFAILED TASKS:\n")
	if len(failed) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, t := range failed {
		b.WriteString(fmt.Sprintf("- [%s] %s (id: %s)\n", t.Type, t.Title, t.ID))
		errMsg, _ := t.GetLastError()
		if errMsg == "" {
			if r := t.GetResult(); r != nil && len(r.Errors) > 0 {
				errMsg = strings.Join(r.Errors, "; ")
			}
		}
		if errMsg != "" {
			b.WriteString(fmt.Sprintf("  Error: %s\n", truncate(errMsg, 300)))
		}
	}

	b.WriteString("\nPENDING/READY TASKS:\n")
	if len(pending) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, t := range pending {
		b.WriteString(fmt.Sprintf("- [%s] %s (id: %s)\n", t.Type, t.Title, t.ID))
	}

	b.WriteString("\nINSTRUCTIONS:\n")
	b.WriteString("Given the original objective and the work completed so far, are additional tasks needed?\n")
	b.WriteString("Should any approach be modified due to failures or new information from completed tasks?\n\n")
	b.WriteString("Output a JSON array of NEW tasks to add, or an empty array [] if no additional work is needed.\n")
	b.WriteString("Do NOT include tasks that already exist.\n")
	b.WriteString("Each task object should have: id, type, title, description, priority, depends_on, max_retries.\n")
	b.WriteString("Output ONLY the JSON array, no other text.\n")

	return b.String()
}

// taskOutput retrieves the output for a completed task from the blackboard.
func (q *Queen) taskOutput(t *task.Task) string {
	if r := t.GetResult(); r != nil && r.Output != "" {
		return r.Output
	}
	// Fallback: check blackboard
	key := fmt.Sprintf("result-%s", t.ID)
	if entry, ok := q.board.Read(key); ok {
		if s, ok := entry.Value.(string); ok {
			return s
		}
	}
	return ""
}
