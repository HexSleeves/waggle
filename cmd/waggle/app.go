package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/urfave/cli/v3"
)

// version is set via ldflags at build time by GoReleaser.
// e.g. -ldflags "-X main.version=1.2.3"
var version = "dev"

// newApp creates the CLI application with all flags and commands.
func newApp() *cli.Command {
	return &cli.Command{
		Name:        "waggle",
		Usage:       "Agent Orchestration System",
		Version:     version,
		UsageText:   "waggle [global options] command [command options] [arguments...]",
		Description: "Waggle orchestrates AI agents to accomplish complex objectives",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "legacy",
				Usage: "Force legacy orchestration loop (no agent mode)",
			},
			&cli.BoolFlag{
				Name:  "plain",
				Usage: "Plain log output (no TUI)",
			},
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "Path to config file",
				Value:   "waggle.json",
			},
			&cli.StringFlag{
				Name:    "project",
				Aliases: []string{"p"},
				Usage:   "Project directory",
				Value:   ".",
			},
			&cli.StringFlag{
				Name:    "adapter",
				Aliases: []string{"a"},
				Usage:   "Default adapter: claude-code, codex, opencode, exec",
			},
			&cli.IntFlag{
				Name:    "workers",
				Aliases: []string{"w"},
				Usage:   "Max parallel workers",
				Value:   4,
			},
			&cli.StringFlag{
				Name:  "tasks",
				Usage: "Load pre-defined tasks from a JSON file",
			},
			&cli.BoolFlag{
				Name:    "verbose",
				Aliases: []string{"v"},
				Usage:   "Verbose logging",
			},
			&cli.BoolFlag{
				Name:  "quiet",
				Usage: "Suppress all output (mutually exclusive with --json and --plain)",
			},
			&cli.BoolFlag{
				Name:  "json",
				Usage: "Output in JSON format (mutually exclusive with --quiet and --plain)",
			},
			&cli.BoolFlag{
				Name:  "dry-run",
				Usage: "Plan tasks without executing workers (shows planned task graph)",
			},
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			// Validate mutual exclusivity of output format flags
			quiet := cmd.Bool("quiet")
			json := cmd.Bool("json")
			plain := cmd.Bool("plain")

			flagCount := 0
			if quiet {
				flagCount++
			}
			if json {
				flagCount++
			}
			if plain {
				flagCount++
			}

			if flagCount > 1 {
				return ctx, fmt.Errorf("flags --quiet, --json, and --plain are mutually exclusive")
			}

			return ctx, nil
		},
		Commands: []*cli.Command{
			{
				Name:   "run",
				Usage:  "Run the queen with an objective",
				Action: cmdRun,
			},
			{
				Name:   "resume",
				Usage:  "Resume an interrupted session",
				Action: cmdResume,
			},
			{
				Name:   "status",
				Usage:  "Show status of current hive session",
				Action: cmdStatus,
			},
			{
				Name:   "init",
				Usage:  "Initialize a .hive directory",
				Action: cmdInit,
			},
			{
				Name:   "config",
				Usage:  "Show current configuration",
				Action: cmdConfig,
			},
			{
				Name:      "logs",
				Usage:     "Show event log for a session",
				ArgsUsage: "[session-id]",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "follow", Aliases: []string{"f"}, Usage: "Follow log output"},
					&cli.IntFlag{Name: "limit", Aliases: []string{"n"}, Value: 100, Usage: "Number of events to show"},
				},
				Action: cmdLogs,
			},
			{
				Name:      "kill",
				Usage:     "Stop a running session",
				ArgsUsage: "<session-id>",
				Action:    cmdKill,
			},
			{
				Name:  "sessions",
				Usage: "List past sessions",
				Flags: []cli.Flag{
					&cli.IntFlag{Name: "limit", Value: 20, Usage: "Maximum sessions to show"},
					&cli.BoolFlag{Name: "running", Usage: "Show only running sessions"},
					&cli.BoolFlag{Name: "remove", Aliases: []string{"rm"}, Usage: "Remove a session", Action: cmdRemoveSession},
				},
				Action: cmdSessions,
			},
			{
				Name:      "dag",
				Usage:     "Show task dependency graph",
				ArgsUsage: "[session-id]",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "ascii", Usage: "ASCII output (default is DOT/Graphviz)"},
					&cli.StringFlag{Name: "session", Aliases: []string{"s"}, Usage: "Session ID"},
				},
				Action: cmdDAG,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			// Default action: treat remaining args as objective (implicit run)
			args := cmd.Args().Slice()
			if len(args) == 0 {
				// No objective: start interactive TUI
				return runObjective(ctx, cmd, "")
			}
			objective := strings.Join(args, " ")
			return runObjective(ctx, cmd, objective)
		},
	}
}
