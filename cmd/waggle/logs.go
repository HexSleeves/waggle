package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/exedev/waggle/internal/state"
	"github.com/urfave/cli/v3"
)

func cmdLogs(ctx context.Context, cmd *cli.Command) error {
	projectDir := cmd.String("project")
	jsonOutput := cmd.Bool("json")
	follow := cmd.Bool("follow")
	limit := cmd.Int("limit")

	hiveDir := filepath.Join(projectDir, ".hive")

	dbPath := filepath.Join(hiveDir, "hive.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("no hive database found. Run 'waggle run <objective>' first")
	}

	db, err := state.OpenDB(hiveDir)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	// Get session ID from args or use latest
	var sessionID string
	args := cmd.Args().Slice()
	if len(args) > 0 {
		sessionID = args[0]
	} else {
		session, err := db.LatestSession(ctx)
		if err != nil {
			return fmt.Errorf("no sessions found. Run 'waggle run <objective>' first")
		}
		sessionID = session.ID
	}

	// Fetch events
	events, err := db.ListEvents(ctx, sessionID, int(limit), 0)
	if err != nil {
		return fmt.Errorf("list events: %w", err)
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(events)
	}

	// Print events
	for _, e := range events {
		printEvent(e)
	}

	// Follow mode: poll for new events
	if follow {
		var lastID int64
		if len(events) > 0 {
			lastID = events[len(events)-1].ID
		}
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(1 * time.Second):
				newEvents, err := db.ListEvents(ctx, sessionID, 100, lastID)
				if err != nil {
					continue
				}
				for _, e := range newEvents {
					printEvent(e)
					lastID = e.ID
				}
			}
		}
	}

	return nil
}

func printEvent(e state.EventRow) {
	// Parse timestamp for display
	ts := e.CreatedAt
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		ts = t.Format("15:04:05.000")
	}

	// Format event type with emoji
	icon := eventIcon(e.Type)

	// Extract useful info from data JSON
	detail := summarizeEventData(e.Type, e.Data)

	fmt.Printf("%s %s %s %s\n", ts, icon, e.Type, detail)
}

func eventIcon(eventType string) string {
	switch eventType {
	case "task.created":
		return "📋"
	case "task.status_changed":
		return "🔄"
	case "task.assigned":
		return "🐝"
	case "worker.spawned":
		return "🚀"
	case "worker.completed":
		return "✅"
	case "worker.failed":
		return "❌"
	case "worker.output":
		return "📝"
	case "blackboard.update":
		return "📌"
	case "queen.decision":
		return "👑"
	case "queen.plan":
		return "📐"
	case "system.error":
		return "⚠️"
	default:
		return "•"
	}
}

func summarizeEventData(eventType, data string) string {
	if data == "" {
		return ""
	}

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return ""
	}

	// Extract task_id and worker_id if present
	var parts []string
	if tid, ok := m["task_id"].(string); ok && tid != "" {
		parts = append(parts, fmt.Sprintf("task=%s", tid))
	}
	if wid, ok := m["worker_id"].(string); ok && wid != "" {
		parts = append(parts, fmt.Sprintf("worker=%s", wid))
	}

	// For status changes, show old->new
	if eventType == "task.status_changed" {
		if payload, ok := m["payload"].(map[string]interface{}); ok {
			if newStatus, ok := payload["new"].(string); ok {
				parts = append(parts, fmt.Sprintf("→ %s", newStatus))
			}
		}
	}

	return strings.Join(parts, " ")
}
