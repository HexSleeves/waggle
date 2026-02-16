package queen

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HexSleeves/waggle/internal/output"
	"github.com/HexSleeves/waggle/internal/task"
)

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
			Status:      t.GetStatus(),
			Result:      t.GetResult(),
			WorkerID:    t.GetWorkerID(),
			CompletedAt: t.CompletedAt,
		}
		results = append(results, tr)
	}
	return results
}

// printReport outputs a complete report of all task results
func (q *Queen) printReport() {
	if q.suppressReport {
		return
	}
	results := q.Results()
	if len(results) == 0 {
		return
	}

	p := output.NewPrinter(output.ModePlain, false)

	p.Header("Final Report")

	completed := 0
	failed := 0
	for _, r := range results {
		if r.Status == task.StatusComplete {
			completed++
		} else if r.Status == task.StatusFailed {
			failed++
		}
	}

	p.KeyValue([][]string{
		{"Objective", q.objective},
	})
	p.Println("")

	p.Table(
		[]string{"Metric", "Count"},
		[][]string{
			{"Completed", fmt.Sprintf("%d", completed)},
			{"Failed", fmt.Sprintf("%d", failed)},
			{"Total", fmt.Sprintf("%d", len(results))},
		},
	)
	p.Println("")

	for _, r := range results {
		icon := output.StatusIcon(string(r.Status))
		p.Section(fmt.Sprintf("%s [%s] %s", icon, r.Type, r.Title))

		if r.Result != nil && r.Result.Output != "" {
			for _, line := range strings.Split(strings.TrimSpace(r.Result.Output), "\n") {
				p.Printf("  %s\n", line)
			}
		} else if r.Result != nil && len(r.Result.Errors) > 0 {
			for _, e := range r.Result.Errors {
				if e != "" {
					p.Error("%s", e)
				}
			}
		} else {
			p.Printf("  (no output)\n")
		}
	}

	p.Divider()
	p.KeyValue([][]string{
		{"Session", q.sessionID},
		{"Log", ".hive/hive.db"},
	})
	p.Println("")
}

// --- Utility helpers ---

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

func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "..."
}
