package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/HexSleeves/waggle/internal/state"
	"github.com/HexSleeves/waggle/internal/task"
	"github.com/urfave/cli/v3"
)

func cmdDAG(ctx context.Context, cmd *cli.Command) error {
	projectDir := cmd.String("project")
	hiveDir := filepath.Join(projectDir, ".hive")

	if _, err := os.Stat(hiveDir); os.IsNotExist(err) {
		return fmt.Errorf("no hive found \u2014 run 'waggle init' first")
	}

	db, err := state.OpenDB(hiveDir)
	if err != nil {
		return fmt.Errorf("open DB: %w", err)
	}
	defer db.Close()

	// Determine session ID
	sessionID := cmd.String("session")
	if sessionID == "" {
		if args := cmd.Args().Slice(); len(args) > 0 {
			sessionID = args[0]
		}
	}
	if sessionID == "" {
		session, err := db.LatestSession(ctx)
		if err != nil {
			return fmt.Errorf("no sessions found")
		}
		sessionID = session.ID
	}

	// Load tasks
	taskRows, err := db.GetTasks(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("load tasks: %w", err)
	}
	if len(taskRows) == 0 {
		fmt.Println("No tasks in session", sessionID)
		return nil
	}

	// Build task graph
	graph := task.NewTaskGraph(nil)
	for _, tr := range taskRows {
		var deps []string
		if tr.DependsOn != "" {
			deps = strings.Split(tr.DependsOn, ",")
		}
		graph.Add(&task.Task{
			ID:        tr.ID,
			Title:     tr.Title,
			Type:      task.Type(tr.Type),
			Status:    task.Status(tr.Status),
			DependsOn: deps,
		})
	}

	if cmd.Bool("ascii") {
		fmt.Print(graph.RenderASCII(80))
	} else {
		fmt.Print(graph.RenderDOT())
	}

	return nil
}
