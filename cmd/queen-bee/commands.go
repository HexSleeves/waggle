package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/exedev/queen-bee/internal/config"
	"github.com/exedev/queen-bee/internal/queen"
	"github.com/exedev/queen-bee/internal/state"
	"github.com/urfave/cli/v3"
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
		return fmt.Errorf("usage: queen-bee run <objective>")
	}

	objective := args[0]
	return runObjective(ctx, cmd, objective)
}

func runObjective(ctx context.Context, cmd *cli.Command, objective string) error {
	logger := log.New(os.Stderr, "", log.LstdFlags)
	cfg := loadConfigFromCtx(ctx, cmd)

	verbose := cmd.Bool("verbose")
	if verbose {
		logger.SetFlags(log.LstdFlags | log.Lmicroseconds)
	}

	tasksFile := cmd.String("tasks")

	fmt.Println("")
	fmt.Println("══════════════════════════════════════════════════")
	fmt.Println("  Queen Bee - Agent Orchestration System")
	fmt.Println("══════════════════════════════════════════════════")
	fmt.Printf("  Objective: %s\n", objective)
	fmt.Printf("  Adapter:   %s\n", cfg.Workers.DefaultAdapter)
	fmt.Printf("  Workers:   %d max parallel\n", cfg.Workers.MaxParallel)
	fmt.Println("")

	q, err := queen.New(cfg, logger)
	if err != nil {
		return fmt.Errorf("init queen: %w", err)
	}
	defer q.Close()

	// Load pre-defined tasks if provided
	if tasksFile != "" {
		tasks, err := loadTasksFile(tasksFile, cfg)
		if err != nil {
			return fmt.Errorf("load tasks file: %w", err)
		}
		q.SetTasks(tasks)
		logger.Printf("Loaded %d tasks from %s", len(tasks), tasksFile)
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

	if err := q.Run(runCtx, objective); err != nil {
		return fmt.Errorf("queen failed: %w", err)
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
		return fmt.Errorf("no session to resume. Run 'queen-bee run <objective>' first")
	}

	// Open the database and find the latest resumable session
	db, err := state.OpenDB(hiveDir)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	session, err := db.FindResumableSession(ctx)
	if err != nil {
		return fmt.Errorf("no interrupted session found to resume. Run 'queen-bee run <objective>' to start a new session")
	}

	logger.Printf("Resuming session: %s", session.ID)
	logger.Printf("   Objective: %s", session.Objective)
	logger.Printf("   Status: %s", session.Status)

	if verbose {
		logger.SetFlags(log.LstdFlags | log.Lmicroseconds)
	}

	fmt.Println("")
	fmt.Println("══════════════════════════════════════════════════")
	fmt.Println("  Queen Bee - Agent Orchestration System")
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

	// Run with the resumed session
	if err := q.Run(runCtx, objective); err != nil {
		return fmt.Errorf("queen failed: %w", err)
	}

	fmt.Println("")
	fmt.Println("══════════════════════════════════════════════════")
	fmt.Println("  Mission Complete")
	fmt.Println("══════════════════════════════════════════════════")
	return nil
}
