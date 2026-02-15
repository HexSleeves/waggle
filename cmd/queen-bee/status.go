package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/exedev/queen-bee/internal/state"
	"github.com/urfave/cli/v3"
)


func cmdStatus(ctx context.Context, cmd *cli.Command) error {
	projectDir := cmd.String("project")
	logger := log.New(os.Stderr, "", log.LstdFlags)
	hiveDir := filepath.Join(projectDir, ".hive")

	if _, err := os.Stat(hiveDir); os.IsNotExist(err) {
		logger.Println("No active hive session. Run 'queen-bee init' first.")
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
	db, err := state.OpenDB(hiveDir)
	if err != nil {
		return fmt.Errorf("open DB: %w", err)
	}
	defer db.Close()

	session, err := db.LatestSession(ctx)
	if err != nil {
		fmt.Println("Hive initialized but no sessions run yet.")
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

	fmt.Println("")
	fmt.Println("══════════════════════════════════════════════════")
	fmt.Println("  Queen Bee — Session Status")
	fmt.Println("══════════════════════════════════════════════════")
	fmt.Printf("  Session:    %s\n", session.ID)
	fmt.Printf("  Objective:  %s\n", session.Objective)
	fmt.Printf("  Status:     %s\n", session.Status)
	fmt.Printf("  Started:    %s\n", session.CreatedAt)
	fmt.Printf("  Updated:    %s\n", session.UpdatedAt)
	fmt.Printf("  Events:     %d\n", eventCount)
	fmt.Println("")
	fmt.Printf("  Tasks: %d total\n", total)
	for _, st := range []string{"complete", "running", "pending", "failed", "cancelled", "retrying"} {
		if c, ok := counts[st]; ok && c > 0 {
			icon := statusIcon(st)
			fmt.Printf("    %s %-10s %d\n", icon, st, c)
		}
	}
	fmt.Println("")

	if len(tasks) > 0 {
		fmt.Println("  Task Details:")
		for _, t := range tasks {
			icon := statusIcon(t.Status)
			worker := ""
			if t.WorkerID != nil && *t.WorkerID != "" {
				worker = fmt.Sprintf(" (worker: %s)", *t.WorkerID)
			}
			fmt.Printf("    %s [%s] %s%s\n", icon, t.Type, t.Title, worker)
		}
		fmt.Println("")
	}
	return nil
}

func statusIcon(st string) string {
	switch st {
	case "complete":
		return "✅"
	case "running":
		return "🔄"
	case "pending":
		return "⏳"
	case "failed":
		return "❌"
	case "cancelled":
		return "⛔"
	case "retrying":
		return "🔁"
	default:
		return "❓"
	}
}
