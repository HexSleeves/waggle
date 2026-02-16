package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/HexSleeves/waggle/internal/output"
	"github.com/HexSleeves/waggle/internal/state"
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

	sessions, err := db.ListSessions(ctx, int(limit), onlyRunning)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(sessions)
	}

	p := output.NewPrinter(output.ModePlain, false)

	if len(sessions) == 0 {
		if onlyRunning {
			p.Info("No running sessions.")
		} else {
			p.Info("No sessions found. Run 'waggle run <objective>' to start one.")
		}
		return nil
	}

	p.Header("Sessions")

	var rows [][]string
	for _, s := range sessions {
		obj := s.Objective
		if len(obj) > 40 {
			obj = obj[:37] + "..."
		}
		status := s.Status
		if s.Status != "done" && s.Status != "cancelled" {
			status = output.StatusIcon(s.Status) + " " + s.Status
		}
		rows = append(rows, []string{
			s.ID,
			status,
			fmt.Sprintf("%d", s.CompletedTasks),
			fmt.Sprintf("%d", s.FailedTasks),
			fmt.Sprintf("%d", s.PendingTasks),
			obj,
		})
	}
	p.Table(
		[]string{"Session", "Status", "Done", "Fail", "Pend", "Objective"},
		rows,
	)
	p.Printf("\n%d session(s)\n", len(sessions))
	return nil
}

func cmdRemoveSession(ctx context.Context, cmd *cli.Command, _ bool) error {
	projectDir := cmd.String("project")
	sessionID := cmd.Args().First()
	if sessionID == "" {
		return fmt.Errorf("session ID required: waggle sessions --remove <session-id>")
	}

	hiveDir := filepath.Join(projectDir, ".hive")
	db, err := state.OpenDB(hiveDir)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	if err := db.RemoveSession(ctx, sessionID); err != nil {
		return fmt.Errorf("remove session: %w", err)
	}

	fmt.Printf("Session %s removed\n", sessionID)
	return nil
}
