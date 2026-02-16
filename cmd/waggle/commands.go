package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/exedev/waggle/internal/bus"
	"github.com/exedev/waggle/internal/config"
	"github.com/exedev/waggle/internal/output"
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

	// Propagate output mode flags from CLI
	cfg.Output.Quiet = cmd.Bool("quiet")
	cfg.Output.JSON = cmd.Bool("json")
	cfg.Output.Plain = cmd.Bool("plain")

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
	objective := ""
	if len(args) > 0 {
		objective = strings.Join(args, " ")
	}
	return runObjective(ctx, cmd, objective)
}

func runObjective(ctx context.Context, cmd *cli.Command, objective string) error {
	cfg := loadConfigFromCtx(ctx, cmd)
	tasksFile := cmd.String("tasks")
	forceLegacy := cmd.Bool("legacy")
	forcePlain := cmd.Bool("plain")
	forceJSON := cmd.Bool("json")

	// JSON mode takes precedence
	if forceJSON {
		return runJSON(ctx, cmd, cfg, objective, tasksFile, forceLegacy)
	}

	// Decide: TUI or plain mode
	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	useTUI := isTTY && !forcePlain

	// Interactive mode: no objective provided, start TUI with input prompt
	if objective == "" && useTUI {
		return runInteractiveTUI(ctx, cfg, tasksFile, forceLegacy)
	}

	if objective == "" {
		return fmt.Errorf("usage: waggle run <objective>")
	}

	if useTUI {
		return runWithTUI(ctx, cfg, objective, tasksFile, forceLegacy)
	}
	return runPlain(ctx, cmd, cfg, objective, tasksFile, forceLegacy)
}

func runInteractiveTUI(ctx context.Context, cfg *config.Config, tasksFile string, forceLegacy bool) error {
	maxTurns := cfg.Queen.MaxIterations
	if maxTurns <= 0 {
		maxTurns = 50
	}

	tuiProg, objectiveCh := tui.NewInteractiveProgram(maxTurns)

	// Wait for the user to submit an objective, then boot the Queen
	go func() {
		objective, ok := <-objectiveCh
		if !ok || objective == "" {
			return
		}

		logger := log.New(tuiProg.LogWriter(), "", log.LstdFlags)

		q, err := queen.New(cfg, logger)
		if err != nil {
			tuiProg.SendDone(false, "", fmt.Sprintf("init queen: %v", err))
			return
		}
		q.SetLogger(logger)
		q.SuppressReport()

		subscribeBusEvents(q, tuiProg)

		if tasksFile != "" {
			tasks, err := loadTasksFile(tasksFile, cfg)
			if err != nil {
				q.Close()
				tuiProg.SendDone(false, "", fmt.Sprintf("load tasks: %v", err))
				return
			}
			q.SetTasks(tasks)
		}

		// startQueen handles the run goroutine + polling; we just wait
		startQueen(ctx, q, tuiProg, objective, forceLegacy)
	}()

	if _, err := tuiProg.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}
	return nil
}

// subscribeBusEvents wires Queen bus events to the TUI program.
func subscribeBusEvents(q *queen.Queen, tuiProg *tui.Program) {
	q.Bus().Subscribe(bus.MsgTaskCreated, func(msg bus.Message) {
		if t, ok := msg.Payload.(*task.Task); ok {
			tuiProg.SendTaskUpdate(t.ID, t.Title, string(t.Type), string(t.GetStatus()), "")
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
		for wid, output := range q.ActiveWorkerOutputs() {
			if output != "" {
				tuiProg.SendWorkerOutput(wid, output)
			}
		}
		tuiProg.Send(tui.WorkerUpdateMsg{
			ID: msg.WorkerID, TaskID: msg.TaskID, Status: "done",
		})
	})
	q.Bus().Subscribe(bus.MsgWorkerFailed, func(msg bus.Message) {
		for wid, output := range q.ActiveWorkerOutputs() {
			if output != "" {
				tuiProg.SendWorkerOutput(wid, output)
			}
		}
		tuiProg.Send(tui.WorkerUpdateMsg{
			ID: msg.WorkerID, TaskID: msg.TaskID, Status: "failed",
		})
	})
}

// pollWorkerOutputs periodically sends worker output snapshots to the TUI.
func pollWorkerOutputs(ctx context.Context, q *queen.Queen, tuiProg *tui.Program) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			for wid, output := range q.ActiveWorkerOutputs() {
				tuiProg.SendWorkerOutput(wid, output)
			}
			return
		case <-ticker.C:
			for wid, output := range q.ActiveWorkerOutputs() {
				if output != "" {
					tuiProg.SendWorkerOutput(wid, output)
				}
			}
		}
	}
}

// startQueen runs the Queen in a goroutine and sends Done when finished.
func startQueen(ctx context.Context, q *queen.Queen, tuiProg *tui.Program, objective string, forceLegacy bool) (context.CancelFunc, <-chan error) {
	return startQueenWithFunc(ctx, q, tuiProg, func(runCtx context.Context) error {
		if !forceLegacy && q.SupportsAgentMode() {
			return q.RunAgent(runCtx, objective)
		}
		return q.Run(runCtx, objective)
	})
}

// startQueenResume runs the Queen in resume mode in a goroutine and sends Done when finished.
func startQueenResume(ctx context.Context, q *queen.Queen, tuiProg *tui.Program, sessionID, objective string, forceLegacy bool) (context.CancelFunc, <-chan error) {
	return startQueenWithFunc(ctx, q, tuiProg, func(runCtx context.Context) error {
		if !forceLegacy && q.SupportsAgentMode() {
			return q.RunAgentResume(runCtx, sessionID)
		}
		return q.Run(runCtx, objective)
	})
}

// startQueenWithFunc runs the given function in a goroutine with output polling and sends Done when finished.
func startQueenWithFunc(ctx context.Context, q *queen.Queen, tuiProg *tui.Program, runFunc func(context.Context) error) (context.CancelFunc, <-chan error) {
	runCtx, cancel := context.WithCancel(ctx)
	errCh := make(chan error, 1)

	go pollWorkerOutputs(runCtx, q, tuiProg)

	go func() {
		defer cancel()
		defer q.Close()

		err := runFunc(runCtx)

		if err != nil {
			tuiProg.SendDone(false, "", err.Error())
		} else {
			tuiProg.SendDone(true, "Objective complete", "")
		}
		errCh <- err
	}()

	return cancel, errCh
}

func runWithTUI(ctx context.Context, cfg *config.Config, objective, tasksFile string, forceLegacy bool) error {
	maxTurns := cfg.Queen.MaxIterations
	if maxTurns <= 0 {
		maxTurns = 50
	}
	tuiProg := tui.NewProgram(objective, maxTurns)

	logger := log.New(tuiProg.LogWriter(), "", log.LstdFlags)

	q, err := queen.New(cfg, logger)
	if err != nil {
		return fmt.Errorf("init queen: %w", err)
	}
	q.SetLogger(logger)
	q.SuppressReport()

	subscribeBusEvents(q, tuiProg)

	if tasksFile != "" {
		tasks, err := loadTasksFile(tasksFile, cfg)
		if err != nil {
			q.Close()
			return fmt.Errorf("load tasks file: %w", err)
		}
		q.SetTasks(tasks)
	}

	cancel, errCh := startQueen(ctx, q, tuiProg, objective, forceLegacy)

	if _, err := tuiProg.Run(); err != nil {
		cancel()
		return fmt.Errorf("TUI error: %w", err)
	}

	return <-errCh
}

func runPlain(ctx context.Context, cmd *cli.Command, cfg *config.Config, objective, tasksFile string, forceLegacy bool) error {
	logger := log.New(os.Stderr, "", log.LstdFlags)

	verbose := cmd.Bool("verbose")
	quiet := cmd.Bool("quiet")
	if verbose {
		logger.SetFlags(log.LstdFlags | log.Lmicroseconds)
	}

	// Suppress banner in quiet mode
	if !quiet {
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
	}

	q, err := queen.New(cfg, logger)
	if err != nil {
		return fmt.Errorf("init queen: %w", err)
	}
	defer q.Close()

	// Propagate quiet flag to Queen
	q.SetQuiet(quiet)

	if tasksFile != "" {
		tasks, err := loadTasksFile(tasksFile, cfg)
		if err != nil {
			return fmt.Errorf("load tasks file: %w", err)
		}
		q.SetTasks(tasks)
		if !quiet {
			logger.Printf("Loaded %d tasks from %s", len(tasks), tasksFile)
		}
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
		if !quiet {
			logger.Println("✓ Agent mode: Queen will use tools autonomously")
		}
		runErr = q.RunAgent(runCtx, objective)
	} else {
		if forceLegacy {
			if !quiet {
				logger.Println("⚙ Legacy mode (--legacy flag)")
			}
		} else {
			provider := cfg.Queen.Provider
			if provider == "" {
				if !quiet {
					logger.Println("⚙ Legacy mode (no queen.provider configured)")
					logger.Println("  💡 Set queen.provider to \"anthropic\" in waggle.json for agent mode")
				}
			} else {
				if !quiet {
					logger.Printf("⚙ Legacy mode (provider %q is CLI-based, no tool support)", provider)
					logger.Println("  💡 For agent mode, set queen.provider to \"anthropic\" (requires ANTHROPIC_API_KEY)")
				}
			}
		}
		runErr = q.Run(runCtx, objective)
	}
	if runErr != nil {
		return fmt.Errorf("queen failed: %w", runErr)
	}

	if !quiet {
		fmt.Println("")
		fmt.Println("══════════════════════════════════════════════════")
		fmt.Println("  Mission Complete")
		fmt.Println("══════════════════════════════════════════════════")
	}
	return nil
}

func runJSON(ctx context.Context, cmd *cli.Command, cfg *config.Config, objective, tasksFile string, forceLegacy bool) error {
	// Create JSON writer for structured output
	jsonWriter := output.NewJSONWriter(os.Stdout, "")

	// Emit session start event
	err := jsonWriter.WriteSessionStart(objective, map[string]interface{}{
		"adapter":     cfg.Workers.DefaultAdapter,
		"max_workers": cfg.Workers.MaxParallel,
		"provider":    cfg.Queen.Provider,
		"model":       cfg.Queen.Model,
	})
	if err != nil {
		return fmt.Errorf("json output: %w", err)
	}

	// Logger writes to stderr (not stdout, to keep stdout valid JSON)
	logger := log.New(os.Stderr, "", log.LstdFlags)

	verbose := cmd.Bool("verbose")
	if verbose {
		logger.SetFlags(log.LstdFlags | log.Lmicroseconds)
	}

	q, err := queen.New(cfg, logger)
	if err != nil {
		_ = jsonWriter.WriteError(fmt.Sprintf("init queen: %v", err), "", "", "initialization_error")
		return fmt.Errorf("init queen: %w", err)
	}
	defer q.Close()

	// Suppress report output in JSON mode (we emit our own JSON summary)
	q.SuppressReport()

	// Update session ID in JSON writer once available
	// Note: session ID is created during Run/RunAgent

	if tasksFile != "" {
		tasks, err := loadTasksFile(tasksFile, cfg)
		if err != nil {
			_ = jsonWriter.WriteError(fmt.Sprintf("load tasks file: %v", err), "", "", "file_error")
			return fmt.Errorf("load tasks file: %w", err)
		}
		q.SetTasks(tasks)
		logger.Printf("Loaded %d tasks from %s", len(tasks), tasksFile)
	}

	// Subscribe to bus events for JSON output
	q.Bus().Subscribe(bus.MsgTaskCreated, func(msg bus.Message) {
		if t, ok := msg.Payload.(*task.Task); ok {
			_ = jsonWriter.WriteTaskCreated(t)
		}
	})
	q.Bus().Subscribe(bus.MsgTaskStatusChanged, func(msg bus.Message) {
		if payload, ok := msg.Payload.(map[string]task.Status); ok {
			_ = jsonWriter.WriteTaskUpdated(msg.TaskID, string(payload["new"]), "")
		}
	})
	q.Bus().Subscribe(bus.MsgTaskAssigned, func(msg bus.Message) {
		_ = jsonWriter.WriteTaskUpdated(msg.TaskID, "running", msg.WorkerID)
	})
	q.Bus().Subscribe(bus.MsgWorkerSpawned, func(msg bus.Message) {
		_ = jsonWriter.WriteWorkerSpawned(msg.WorkerID, msg.TaskID)
	})
	q.Bus().Subscribe(bus.MsgWorkerCompleted, func(msg bus.Message) {
		history := q.Bus().History(1)
		if len(history) > 0 {
			if t, ok := history[0].Payload.(*task.Task); ok {
				_ = jsonWriter.WriteTaskCompleted(t)
			}
		}
		// Capture final worker output
		for wid, out := range q.ActiveWorkerOutputs() {
			if out != "" {
				_ = jsonWriter.WriteWorkerOutput(wid, out)
			}
		}
	})
	q.Bus().Subscribe(bus.MsgWorkerFailed, func(msg bus.Message) {
		// Capture final worker output before cleanup
		for wid, out := range q.ActiveWorkerOutputs() {
			if out != "" {
				_ = jsonWriter.WriteWorkerOutput(wid, out)
			}
		}
	})

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		logger.Println("\nReceived shutdown signal, gracefully stopping...")
		_ = jsonWriter.WriteError("shutdown signal received", "", "", "interrupted")
		cancel()
	}()

	var runErr error
	if !forceLegacy && q.SupportsAgentMode() {
		logger.Println("Agent mode: Queen will use tools autonomously")
		runErr = q.RunAgent(runCtx, objective)
	} else {
		runErr = q.Run(runCtx, objective)
	}

	// Get session ID from queen for final output
	status := q.Status()
	if sessionID, ok := status["session_id"].(string); ok && sessionID != "" {
		jsonWriter.SetSessionID(sessionID)
	}

	// Build final summary
	allTasks := q.Results()
	completedCount := 0
	failedCount := 0
	var taskEvents []output.TaskEvent

	for _, tr := range allTasks {
		if tr.Status == task.StatusComplete {
			completedCount++
		} else if tr.Status == task.StatusFailed {
			failedCount++
		}

		te := output.TaskEvent{
			TaskID:      tr.ID,
			Title:       tr.Title,
			Type:        string(tr.Type),
			Status:      string(tr.Status),
			WorkerID:    tr.WorkerID,
			CompletedAt: tr.CompletedAt,
		}
		if tr.Result != nil {
			resultOutput := tr.Result.Output
			if len(resultOutput) > 10000 {
				resultOutput = resultOutput[:10000] + "... [truncated]"
			}
			te.Result = &output.TaskResult{
				Success:   tr.Result.Success,
				Output:    resultOutput,
				Errors:    tr.Result.Errors,
				Artifacts: tr.Result.Artifacts,
				Metrics:   tr.Result.Metrics,
			}
		}
		taskEvents = append(taskEvents, te)
	}

	iterations := 0
	if iter, ok := status["iteration"].(int); ok {
		iterations = iter
	}

	sessionStatus := "done"
	if runErr != nil {
		sessionStatus = "failed"
	}

	summary := output.SessionSummary{
		Objective:      objective,
		Status:         sessionStatus,
		TotalTasks:     len(allTasks),
		CompletedTasks: completedCount,
		FailedTasks:    failedCount,
		Iterations:     iterations,
		Tasks:          taskEvents,
	}

	// Emit final session summary
	if err := jsonWriter.WriteSessionEnd(summary); err != nil {
		logger.Printf("Warning: failed to write final JSON summary: %v", err)
	}

	if runErr != nil {
		return fmt.Errorf("queen failed: %w", runErr)
	}

	return nil
}

func cmdResume(ctx context.Context, cmd *cli.Command) error {
	cfg := loadConfigFromCtx(ctx, cmd)
	projectDir := cmd.String("project")
	forcePlain := cmd.Bool("plain")
	forceLegacy := cmd.Bool("legacy")

	hiveDir := filepath.Join(projectDir, ".hive")
	dbPath := filepath.Join(hiveDir, "hive.db")

	// Check if hive and database exist
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("no session to resume. Run 'waggle run <objective>' first")
	}

	// Open the database and find the latest resumable session.
	// This must happen before creating the TUI because we need the objective.
	db, err := state.OpenDB(hiveDir)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	session, err := db.FindResumableSession(ctx)
	if err != nil {
		return fmt.Errorf("no interrupted session found to resume. Run 'waggle run <objective>' to start a new session")
	}

	// Decide: TUI or plain mode
	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	useTUI := isTTY && !forcePlain

	if useTUI {
		return runResumeTUI(ctx, cfg, session.ID, session.Objective, forceLegacy)
	}
	return runResumePlain(ctx, cmd, cfg, session.ID, session.Objective, forceLegacy)
}

func runResumeTUI(ctx context.Context, cfg *config.Config, sessionID, objective string, forceLegacy bool) error {
	maxTurns := cfg.Queen.MaxIterations
	if maxTurns <= 0 {
		maxTurns = 50
	}
	tuiProg := tui.NewProgram(objective, maxTurns)

	logger := log.New(tuiProg.LogWriter(), "", log.LstdFlags)

	q, err := queen.New(cfg, logger)
	if err != nil {
		return fmt.Errorf("init queen: %w", err)
	}
	q.SetLogger(logger)
	q.SuppressReport()

	// Resume the session so the Queen reloads prior state
	resumedObjective, err := q.ResumeSession(ctx, sessionID)
	if err != nil {
		q.Close()
		return fmt.Errorf("resume session: %w", err)
	}
	// Use the objective returned by ResumeSession (should match, but be safe)
	if resumedObjective != "" {
		objective = resumedObjective
	}

	subscribeBusEvents(q, tuiProg)

	cancel, errCh := startQueenResume(ctx, q, tuiProg, sessionID, objective, forceLegacy)

	if _, err := tuiProg.Run(); err != nil {
		cancel()
		return fmt.Errorf("TUI error: %w", err)
	}

	return <-errCh
}

func runResumePlain(ctx context.Context, cmd *cli.Command, cfg *config.Config, sessionID, objective string, forceLegacy bool) error {
	verbose := cmd.Bool("verbose")
	quiet := cmd.Bool("quiet")
	logger := log.New(os.Stderr, "", log.LstdFlags)

	logger.Printf("Resuming session: %s", sessionID)
	logger.Printf("   Objective: %s", objective)

	if verbose {
		logger.SetFlags(log.LstdFlags | log.Lmicroseconds)
	}

	if !quiet {
		fmt.Println("")
		fmt.Println("══════════════════════════════════════════════════")
		fmt.Println("  Waggle - Agent Orchestration System")
		fmt.Println("  Resuming Interrupted Session")
		fmt.Println("══════════════════════════════════════════════════")
		fmt.Printf("  Session ID: %s\n", sessionID)
		fmt.Printf("  Objective: %s\n", objective)
		fmt.Printf("  Adapter:   %s\n", cfg.Workers.DefaultAdapter)
		fmt.Printf("  Workers:   %d max parallel\n", cfg.Workers.MaxParallel)
		fmt.Println("")
	}

	// Create queen instance
	q, err := queen.New(cfg, logger)
	if err != nil {
		return fmt.Errorf("init queen: %w", err)
	}
	defer q.Close()

	// Resume the session
	resumedObjective, err := q.ResumeSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("resume session: %w", err)
	}
	if resumedObjective != "" {
		objective = resumedObjective
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
	var runErr error
	if !forceLegacy && q.SupportsAgentMode() {
		if !quiet {
			logger.Println("✓ Agent mode: resuming with tool-using Queen")
		}
		runErr = q.RunAgentResume(runCtx, sessionID)
	} else {
		runErr = q.Run(runCtx, objective)
	}
	if runErr != nil {
		return fmt.Errorf("queen failed: %w", runErr)
	}

	if !quiet {
		fmt.Println("")
		fmt.Println("══════════════════════════════════════════════════")
		fmt.Println("  Mission Complete")
		fmt.Println("══════════════════════════════════════════════════")
	}
	return nil
}
