package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/HexSleeves/waggle/internal/output"
	"github.com/HexSleeves/waggle/internal/state"
	"github.com/urfave/cli/v3"
)

func cmdStatus(ctx context.Context, cmd *cli.Command) error {
	projectDir := cmd.String("project")
	logger := log.New(os.Stderr, "", log.LstdFlags)
	hiveDir := filepath.Join(projectDir, ".hive")

	if _, err := os.Stat(hiveDir); os.IsNotExist(err) {
		logger.Println("No active hive session. Run 'waggle init' first.")
		return nil
	}

	dbPath := filepath.Join(hiveDir, "hive.db")

	if _, err := os.Stat(dbPath); err == nil {
		return cmdStatusDB(hiveDir)
	}

	fmt.Println("Hive initialized but no sessions run yet.")
	return nil
}

// cmdStatusDB reads status from the SQLite database.
func cmdStatusDB(hiveDir string) error {
	ctx := context.Background()
	p := output.NewPrinter(output.ModePlain, false)

	db, err := state.OpenDB(hiveDir)
	if err != nil {
		return fmt.Errorf("open DB: %w", err)
	}
	defer db.Close()

	session, err := db.LatestSession(ctx)
	if err != nil {
		p.Info("Hive initialized but no sessions run yet.")
		return nil
	}

	counts, err := db.CountTasksByStatus(ctx, session.ID)
	if err != nil {
		return fmt.Errorf("count tasks: %w", err)
	}

	tasks, err := db.GetTasks(ctx, session.ID)
	if err != nil {
		return fmt.Errorf("get tasks: %w", err)
	}

	eventCount, err := db.EventCount(ctx, session.ID)
	if err != nil {
		return fmt.Errorf("event count: %w", err)
	}

	total := 0
	for _, c := range counts {
		total += c
	}

	// Header
	p.Header("Waggle \u2014 Session Status")

	// Session info
	p.KeyValue([][]string{
		{"Session", session.ID},
		{"Objective", session.Objective},
		{"Status", session.Status},
		{"Started", session.CreatedAt},
		{"Updated", session.UpdatedAt},
		{"Events", fmt.Sprintf("%d", eventCount)},
	})
	p.Println("")

	// Task summary table
	p.Section(fmt.Sprintf("Tasks: %d total", total))
	var rows [][]string
	for _, st := range []string{"complete", "running", "pending", "failed", "cancelled", "retrying"} {
		if c, ok := counts[st]; ok && c > 0 {
			rows = append(rows, []string{output.StatusIcon(st), st, fmt.Sprintf("%d", c)})
		}
	}
	if len(rows) > 0 {
		p.Table([]string{" ", "Status", "Count"}, rows)
	}

	// Task details
	if len(tasks) > 0 {
		p.Section("Task Details")
		var items []output.BulletItem
		for _, t := range tasks {
			text := fmt.Sprintf("[%s] %s", t.Type, t.Title)
			if t.WorkerID != nil && *t.WorkerID != "" {
				text += fmt.Sprintf(" (worker: %s)", *t.WorkerID)
			}
			items = append(items, output.BulletItem{
				Icon: output.StatusIcon(t.Status),
				Text: text,
			})
		}
		p.BulletList(items)
	}

	return nil
}
