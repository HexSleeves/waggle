package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/HexSleeves/waggle/internal/output"
	"github.com/HexSleeves/waggle/internal/state"
	"github.com/urfave/cli/v3"
)

func cmdList(ctx context.Context, cmd *cli.Command) error {
	limit := cmd.Int("limit")
	statusFilter := cmd.String("status")
	typeFilter := cmd.String("type")
	projectDir := cmd.String("project")

	hiveDir := filepath.Join(projectDir, ".hive")

	if _, err := os.Stat(hiveDir); os.IsNotExist(err) {
		return fmt.Errorf("no hive found at %s. Run 'waggle init' first", hiveDir)
	}

	db, err := state.OpenDB(hiveDir)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	session, err := db.LatestSession(ctx)
	if err != nil {
		return fmt.Errorf("no sessions found: %w", err)
	}

	var tasks []state.TaskRow
	if statusFilter != "" {
		tasks, err = db.GetTasksByStatus(ctx, session.ID, statusFilter)
	} else {
		tasks, err = db.GetTasks(ctx, session.ID)
	}
	if err != nil {
		return fmt.Errorf("get tasks: %w", err)
	}

	if typeFilter != "" {
		filtered := make([]state.TaskRow, 0, len(tasks))
		for _, task := range tasks {
			if strings.EqualFold(task.Type, typeFilter) {
				filtered = append(filtered, task)
			}
		}
		tasks = filtered
	}

	if limit > 0 && len(tasks) > limit {
		tasks = tasks[:limit]
	}

	p := output.NewPrinter(output.ModePlain, false)

	if len(tasks) == 0 {
		if statusFilter != "" {
			if typeFilter != "" {
				p.Info("No tasks with status '%s' and type '%s' found.", statusFilter, typeFilter)
			} else {
				p.Info("No tasks with status '%s' found.", statusFilter)
			}
		} else if typeFilter != "" {
			p.Info("No tasks with type '%s' found.", typeFilter)
		} else {
			p.Info("No tasks found.")
		}
		return nil
	}

	p.Header("Tasks")

	var rows [][]string
	for _, t := range tasks {
		desc := t.Title
		if len(desc) > 50 {
			desc = desc[:47] + "..."
		}
		worker := "-"
		if t.WorkerID != nil && *t.WorkerID != "" {
			worker = *t.WorkerID
		}
		rows = append(rows, []string{
			output.StatusIcon(t.Status),
			shortTaskID(t.ID),
			t.Type,
			fmt.Sprintf("%d", t.Priority),
			worker,
			desc,
		})
	}
	p.Table(
		[]string{"Status", "ID", "Type", "Priority", "Worker", "Title"},
		rows,
	)
	p.Printf("\n%d task(s) in session %s\n", len(tasks), session.ID)

	return nil
}

func shortTaskID(id string) string {
	const maxLen = 8
	if len(id) <= maxLen {
		return id
	}
	return id[:maxLen]
}
