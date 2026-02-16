package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/exedev/waggle/internal/state"
	"github.com/urfave/cli/v3"
)

func cmdSessions(ctx context.Context, cmd *cli.Command) error {
	projectDir := cmd.String("project")
	limit := cmd.Int("limit")
	jsonOutput := cmd.Bool("json")
	onlyRunning := cmd.Bool("running")

	hiveDir := filepath.Join(projectDir, ".hive")
	db, err := state.OpenDB(hiveDir)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	sessions, err := db.ListSessions(ctx, int(limit))
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}

	if onlyRunning {
		var filtered []state.SessionSummary
		for _, s := range sessions {
			if s.Status != "done" && s.Status != "cancelled" {
				filtered = append(filtered, s)
			}
		}
		sessions = filtered
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(sessions)
	}

	if len(sessions) == 0 {
		if onlyRunning {
			fmt.Println("No running sessions.")
		} else {
			fmt.Println("No sessions found. Run 'waggle run <objective>' to start one.")
		}
		return nil
	}

	fmt.Printf("%-20s %-10s %-6s %-6s %-6s  %s\n", "SESSION", "STATUS", "DONE", "FAIL", "PEND", "OBJECTIVE")
	fmt.Println(strings.Repeat("─", 80))
	for _, s := range sessions {
		obj := s.Objective
		if len(obj) > 40 {
			obj = obj[:37] + "..."
		}
		sid := s.ID
		// if len(sid) > 20 {
		// 	sid = sid[:17] + "..."
		// }
		status := s.Status
		if s.Status != "done" && s.Status != "cancelled" {
			status = "● " + s.Status
		}
		fmt.Printf("%-20s %-10s %-6d %-6d %-6d  %s\n",
			sid, status, s.CompletedTasks, s.FailedTasks, s.PendingTasks, obj)
	}
	fmt.Printf("\n%d session(s)\n", len(sessions))
	return nil
}
