package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/exedev/waggle/internal/bus"
	"github.com/exedev/waggle/internal/config"
	"github.com/exedev/waggle/internal/queen"
	"github.com/exedev/waggle/internal/state"
	"github.com/exedev/waggle/internal/task"
	"github.com/exedev/waggle/internal/tui"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"
)

func loadConfigFromCtx(ctx context.Context, cmd *cli.Command) *config.Config {
	configPath := cmd.String("config")
	projectDir := cmd.String("project")
	defaultAdapter := cmd.String("adapter")
	maxWorkers := cmd.Int("workers")

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Load config: %v", err)
	}

	if projectDir != "." {
		cfg.ProjectDir = projectDir
	}
	if defaultAdapter != "" {
		cfg.Workers.DefaultAdapter = defaultAdapter
	}
	if maxWorkers > 0 {
		cfg.Workers.MaxParallel = maxWorkers
	}

	return cfg
}

func cmdInit(ctx context.Context, cmd *cli.Command) error {
	projectDir := cmd.String("project")
	configPath := cmd.String("config")
	logger := log.New(os.Stderr, "", log.LstdFlags)

	hiveDir := filepath.Join(projectDir, ".hive")
	if err := os.MkdirAll(hiveDir, 0755); err != nil {
		return fmt.Errorf("create .hive: %w", err)
	}

	cfg := config.DefaultConfig()
	cfg.ProjectDir = projectDir
	if err := cfg.Save(configPath); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	logger.Printf("Initialized hive at %s", hiveDir)
	logger.Printf("Config saved to %s", configPath)
	return nil
}

func cmdConfig(ctx context.Context, cmd *cli.Command) error {
	configPath := cmd.String("config")

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	fmt.Printf("Configuration (%s):\n", configPath)
	fmt.Printf("  Project Dir:     %s\n", cfg.ProjectDir)
	fmt.Printf("  Hive Dir:        %s\n", cfg.HiveDir)
	fmt.Printf("  Queen Model:     %s (%s)\n", cfg.Queen.Model, cfg.Queen.Provider)
	fmt.Printf("  Max Workers:     %d\n", cfg.Workers.MaxParallel)
	fmt.Printf("  Default Adapter: %s\n", cfg.Workers.DefaultAdapter)
	fmt.Printf("  Max Retries:     %d\n", cfg.Workers.MaxRetries)
	fmt.Printf("  Worker Timeout:  %v\n", cfg.Workers.DefaultTimeout)
	fmt.Printf("  Available Adapters:\n")
	for name, a := range cfg.Adapters {
		fmt.Printf("    - %s: %s %v\n", name, a.Command, a.Args)
	}
	return nil
}

func cmdRun(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args().Slice()
	if len(args) == 0 {
		return fmt.Errorf("usage: waggle run <objective>")
	}

	objective := args[0]
	return runObjective(ctx, cmd, objective)
}

func runObjective(ctx context.Context, cmd *cli.Command, objective string) error {
	cfg := loadConfigFromCtx(ctx, cmd)
	tasksFile := cmd.String("tasks")
	forceLegacy := cmd.Bool("legacy")
	forcePlain := cmd.Bool("plain")

	// Decide: TUI or plain mode
	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	useTUI := isTTY && !forcePlain

	if useTUI {
		return runWithTUI(ctx, cfg, objective, tasksFile, forceLegacy)
	}
	return runPlain(ctx, cmd, cfg, objective, tasksFile, forceLegacy)
}

func runWithTUI(ctx context.Context, cfg *config.Config, objective, tasksFile string, forceLegacy bool) error {
	// Create queen with a logger that writes to the TUI
	maxTurns := cfg.Queen.MaxIterations
	if maxTurns <= 0 {
		maxTurns = 50
	}
	tuiProg := tui.NewProgram(objective, maxTurns)

	// Create a logger that routes to the TUI
	logger := log.New(tuiProg.LogWriter(), "", log.LstdFlags)

	q, err := queen.New(cfg, logger)
	if err != nil {
		return fmt.Errorf("init queen: %w", err)
	}

	// Redirect the queen's logger to TUI and suppress direct stdout
	q.SetLogger(logger)
	q.SuppressReport()

	// Subscribe to bus events for task/worker updates
	q.Bus().Subscribe(bus.MsgTaskCreated, func(msg bus.Message) {
		if t, ok := msg.Payload.(*task.Task); ok {
			tuiProg.SendTaskUpdate(t.ID, t.Title, string(t.Type), string(t.Status), "")
		}
	})
	q.Bus().Subscribe(bus.MsgTaskStatusChanged, func(msg bus.Message) {
		if payload, ok := msg.Payload.(map[string]task.Status); ok {
			tuiProg.SendTaskUpdate(msg.TaskID, "", "", string(payload["new"]), "")
		}
	})
	q.Bus().Subscribe(bus.MsgTaskAssigned, func(msg bus.Message) {
		tuiProg.SendTaskUpdate(msg.TaskID, "", "", "running", msg.WorkerID)
	})
	q.Bus().Subscribe(bus.MsgWorkerSpawned, func(msg bus.Message) {
		tuiProg.Send(tui.WorkerUpdateMsg{
			ID: msg.WorkerID, TaskID: msg.TaskID, Status: "running",
		})
	})
	q.Bus().Subscribe(bus.MsgWorkerCompleted, func(msg bus.Message) {
		tuiProg.Send(tui.WorkerUpdateMsg{
			ID: msg.WorkerID, TaskID: msg.TaskID, Status: "done",
		})
	})
	q.Bus().Subscribe(bus.MsgWorkerFailed, func(msg bus.Message) {
		tuiProg.Send(tui.WorkerUpdateMsg{
			ID: msg.WorkerID, TaskID: msg.TaskID, Status: "failed",
		})
	})

	// Load pre-defined tasks
	if tasksFile != "" {
		tasks, err := loadTasksFile(tasksFile, cfg)
		if err != nil {
			q.Close()
			return fmt.Errorf("load tasks file: %w", err)
		}
		q.SetTasks(tasks)
	}

	// Run the queen in a goroutine, TUI in the main thread
	runCtx, cancel := context.WithCancel(ctx)
	var runErr error

	go func() {
		defer cancel()
		defer q.Close()

		if !forceLegacy && q.SupportsAgentMode() {
			runErr = q.RunAgent(runCtx, objective)
		} else {
			runErr = q.Run(runCtx, objective)
		}

		if runErr != nil {
			tuiProg.SendDone(false, "", runErr.Error())
		} else {
			tuiProg.SendDone(true, "Objective complete", "")
		}
	}()

	// Run the TUI (blocks until done)
	if _, err := tuiProg.Run(); err != nil {
		cancel()
		q.Close()
		return fmt.Errorf("TUI error: %w", err)
	}

	return runErr
}

func runPlain(ctx context.Context, cmd *cli.Command, cfg *config.Config, objective, tasksFile string, forceLegacy bool) error {
	logger := log.New(os.Stderr, "", log.LstdFlags)

	verbose := cmd.Bool("verbose")
	if verbose {
		logger.SetFlags(log.LstdFlags | log.Lmicroseconds)
	}

	fmt.Println("")
	fmt.Println("══════════════════════════════════════════════════")
	fmt.Println("  Waggle - Agent Orchestration System")
	fmt.Println("══════════════════════════════════════════════════")
	fmt.Printf("  Objective: %s\n", objective)
	fmt.Printf("  Adapter:   %s\n", cfg.Workers.DefaultAdapter)
	fmt.Printf("  Workers:   %d max parallel\n", cfg.Workers.MaxParallel)
	if cfg.Queen.Provider != "" {
		fmt.Printf("  Queen LLM: %s (%s)\n", cfg.Queen.Provider, cfg.Queen.Model)
	}
	fmt.Println("")

	q, err := queen.New(cfg, logger)
	if err != nil {
		return fmt.Errorf("init queen: %w", err)
	}
	defer q.Close()

	if tasksFile != "" {
		tasks, err := loadTasksFile(tasksFile, cfg)
		if err != nil {
			return fmt.Errorf("load tasks file: %w", err)
		}
		q.SetTasks(tasks)
		logger.Printf("Loaded %d tasks from %s", len(tasks), tasksFile)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		logger.Println("\nReceived shutdown signal, gracefully stopping...")
		cancel()
	}()

	var runErr error
	if !forceLegacy && q.SupportsAgentMode() {
		logger.Println("✓ Agent mode: Queen will use tools autonomously")
		runErr = q.RunAgent(runCtx, objective)
	} else {
		if forceLegacy {
			logger.Println("⚙ Legacy mode (--legacy flag)")
		} else {
			provider := cfg.Queen.Provider
			if provider == "" {
				logger.Println("⚙ Legacy mode (no queen.provider configured)")
				logger.Println("  💡 Set queen.provider to \"anthropic\" in waggle.json for agent mode")
			} else {
				logger.Printf("⚙ Legacy mode (provider %q is CLI-based, no tool support)", provider)
				logger.Println("  💡 For agent mode, set queen.provider to \"anthropic\" (requires ANTHROPIC_API_KEY)")
			}
		}
		runErr = q.Run(runCtx, objective)
	}
	if runErr != nil {
		return fmt.Errorf("queen failed: %w", runErr)
	}

	fmt.Println("")
	fmt.Println("══════════════════════════════════════════════════")
	fmt.Println("  Mission Complete")
	fmt.Println("══════════════════════════════════════════════════")
	return nil
}

func cmdResume(ctx context.Context, cmd *cli.Command) error {
	cfg := loadConfigFromCtx(ctx, cmd)
	projectDir := cmd.String("project")
	verbose := cmd.Bool("verbose")
	logger := log.New(os.Stderr, "", log.LstdFlags)

	hiveDir := filepath.Join(projectDir, ".hive")
	dbPath := filepath.Join(hiveDir, "hive.db")

	// Check if hive and database exist
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("no session to resume. Run 'waggle run <objective>' first")
	}

	// Open the database and find the latest resumable session
	db, err := state.OpenDB(hiveDir)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	session, err := db.FindResumableSession(ctx)
	if err != nil {
		return fmt.Errorf("no interrupted session found to resume. Run 'waggle run <objective>' to start a new session")
	}

	logger.Printf("Resuming session: %s", session.ID)
	logger.Printf("   Objective: %s", session.Objective)
	logger.Printf("   Status: %s", session.Status)

	if verbose {
		logger.SetFlags(log.LstdFlags | log.Lmicroseconds)
	}

	fmt.Println("")
	fmt.Println("══════════════════════════════════════════════════")
	fmt.Println("  Waggle - Agent Orchestration System")
	fmt.Println("  Resuming Interrupted Session")
	fmt.Println("══════════════════════════════════════════════════")
	fmt.Printf("  Session ID: %s\n", session.ID)
	fmt.Printf("  Objective: %s\n", session.Objective)
	fmt.Printf("  Adapter:   %s\n", cfg.Workers.DefaultAdapter)
	fmt.Printf("  Workers:   %d max parallel\n", cfg.Workers.MaxParallel)
	fmt.Println("")

	// Create queen instance
	q, err := queen.New(cfg, logger)
	if err != nil {
		return fmt.Errorf("init queen: %w", err)
	}
	defer q.Close()

	// Resume the session
	objective, err := q.ResumeSession(ctx, session.ID)
	if err != nil {
		return fmt.Errorf("resume session: %w", err)
	}

	// Handle graceful shutdown
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigs
		logger.Println("\nReceived shutdown signal, gracefully stopping...")
		cancel()
	}()

	// Run with the resumed session, preferring agent mode
	forceLegacy := cmd.Bool("legacy")
	var runErr error
	if !forceLegacy && q.SupportsAgentMode() {
		logger.Println("✓ Agent mode: resuming with tool-using Queen")
		runErr = q.RunAgentResume(runCtx, session.ID)
	} else {
		runErr = q.Run(runCtx, objective)
	}
	if runErr != nil {
		return fmt.Errorf("queen failed: %w", runErr)
	}

	fmt.Println("")
	fmt.Println("══════════════════════════════════════════════════")
	fmt.Println("  Mission Complete")
	fmt.Println("══════════════════════════════════════════════════")
	return nil
}
