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

	"github.com/HexSleeves/waggle/internal/bus"
	"github.com/HexSleeves/waggle/internal/config"
	"github.com/HexSleeves/waggle/internal/output"
	"github.com/HexSleeves/waggle/internal/queen"
	"github.com/HexSleeves/waggle/internal/state"
	"github.com/HexSleeves/waggle/internal/task"
	"github.com/HexSleeves/waggle/internal/tui"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"
)

func loadConfigFromCtx(ctx context.Context, cmd *cli.Command) (*config.Config, error) {
	configPath := cmd.String("config")
	projectDir := cmd.String("project")
	defaultAdapter := cmd.String("adapter")
	maxWorkers := cmd.Int("workers")

	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
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

	// Propagate dry-run flag
	cfg.Queen.DryRun = cmd.Bool("dry-run")

	return cfg, nil
}

func cmdInit(ctx context.Context, cmd *cli.Command) error {
	projectDir := cmd.String("project")
	configPath := cmd.String("config")
	p := output.NewPrinter(output.ModePlain, false)

	hiveDir := filepath.Join(projectDir, ".hive")
	if err := os.MkdirAll(hiveDir, 0755); err != nil {
		return fmt.Errorf("create .hive: %w", err)
	}

	cfg := config.DefaultConfig()
	cfg.ProjectDir = projectDir
	// Include example adapter_map so users can see the option
	cfg.Workers.AdapterMap = map[string]string{
		"code":   "claude-code",
		"test":   "claude-code",
		"review": "claude-code",
	}
	if err := cfg.Save(configPath); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	p.Success("Initialized hive at %s", hiveDir)
	p.Success("Config saved to %s", configPath)
	return nil
}

func cmdConfig(ctx context.Context, cmd *cli.Command) error {
	configPath := cmd.String("config")

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	p := output.NewPrinter(output.ModePlain, false)
	p.Section(fmt.Sprintf("Configuration (%s)", configPath))
	p.KeyValue([][]string{
		{"Project Dir", cfg.ProjectDir},
		{"Hive Dir", cfg.HiveDir},
		{"Queen Model", fmt.Sprintf("%s (%s)", cfg.Queen.Model, cfg.Queen.Provider)},
		{"Max Workers", fmt.Sprintf("%d", cfg.Workers.MaxParallel)},
		{"Default Adapter", cfg.Workers.DefaultAdapter},
		{"Max Retries", fmt.Sprintf("%d", cfg.Workers.MaxRetries)},
		{"Worker Timeout", fmt.Sprintf("%v", cfg.Workers.DefaultTimeout)},
	})
	if len(cfg.Workers.AdapterMap) > 0 {
		fmt.Printf("  Adapter Map:\n")
		for taskType, adapterName := range cfg.Workers.AdapterMap {
			fmt.Printf("    %s → %s\n", taskType, adapterName)
		}
	}
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
	cfg, err := loadConfigFromCtx(ctx, cmd)
	if err != nil {
		return err
	}
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

		q, err := newQueenForTUI(cfg, tuiProg)
		if err != nil {
			tuiProg.SendDone(false, "", fmt.Sprintf("init queen: %v", err))
			return
		}

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

// newQueenForTUI creates a Queen wired to a TUI program with logger, report suppression, and bus events.
func newQueenForTUI(cfg *config.Config, tuiProg *tui.Program) (*queen.Queen, error) {
	logger := log.New(tuiProg.LogWriter(), "", log.LstdFlags)
	q, err := queen.New(cfg, logger)
	if err != nil {
		return nil, err
	}
	q.SetLogger(logger)
	q.SuppressReport()
	subscribeBusEvents(q, tuiProg)
	return q, nil
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

	q, err := newQueenForTUI(cfg, tuiProg)
	if err != nil {
		return fmt.Errorf("init queen: %w", err)
	}

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

	// Create styled printer for plain mode
	mode := output.ModePlain
	if quiet {
		mode = output.ModeQuiet
	}
	p := output.NewPrinter(mode, verbose)

	// Print banner
	p.Header("Waggle \u2014 Agent Orchestration")
	p.KeyValue([][]string{
		{"Objective", objective},
		{"Adapter", cfg.Workers.DefaultAdapter},
		{"Workers", fmt.Sprintf("%d max parallel", cfg.Workers.MaxParallel)},
	})
	if cfg.Queen.Provider != "" {
		p.KeyValue([][]string{
			{"Queen LLM", fmt.Sprintf("%s (%s)", cfg.Queen.Provider, cfg.Queen.Model)},
		})
	}
	p.Println("")

	q, err := queen.New(cfg, logger)
	if err != nil {
		return fmt.Errorf("init queen: %w", err)
	}
	defer q.Close()

	// Wire printer and quiet mode
	q.SetPrinter(p)
	q.SetQuiet(quiet)

	if tasksFile != "" {
		tasks, err := loadTasksFile(tasksFile, cfg)
		if err != nil {
			return fmt.Errorf("load tasks file: %w", err)
		}
		q.SetTasks(tasks)
		p.Info("Loaded %d tasks from %s", len(tasks), tasksFile)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)
	go func() {
		<-sigs
		p.Warning("Received shutdown signal, gracefully stopping...")
		cancel()
	}()

	var runErr error
	if !forceLegacy && q.SupportsAgentMode() {
		p.Success("Agent mode: Queen will use tools autonomously")
		runErr = q.RunAgent(runCtx, objective)
	} else {
		if forceLegacy {
			p.Info("Legacy mode (--legacy flag)")
		} else {
			provider := cfg.Queen.Provider
			if provider == "" {
				p.Info("Legacy mode (no queen.provider configured)")
				p.Info("Set queen.provider to \"anthropic\" in waggle.json for agent mode")
			} else {
				p.Info("Legacy mode (provider %q is CLI-based, no tool support)", provider)
				p.Info("For agent mode, set queen.provider to \"anthropic\" (requires ANTHROPIC_API_KEY)")
			}
		}
		runErr = q.Run(runCtx, objective)
	}
	if runErr != nil {
		return fmt.Errorf("queen failed: %w", runErr)
	}

	p.Println("")
	p.Header("Mission Complete")
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
	defer signal.Stop(sigs)
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
	cfg, err := loadConfigFromCtx(ctx, cmd)
	if err != nil {
		return err
	}
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

	q, err := newQueenForTUI(cfg, tuiProg)
	if err != nil {
		return fmt.Errorf("init queen: %w", err)
	}

	// Resume the session so the Queen reloads prior state
	resumedObjective, err := q.ResumeSession(ctx, sessionID)
	if err != nil {
		q.Close()
		return fmt.Errorf("resume session: %w", err)
	}
	if resumedObjective != "" {
		objective = resumedObjective
	}

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

	if verbose {
		logger.SetFlags(log.LstdFlags | log.Lmicroseconds)
	}

	// Create styled printer
	mode := output.ModePlain
	if quiet {
		mode = output.ModeQuiet
	}
	p := output.NewPrinter(mode, verbose)

	p.Header("Waggle \u2014 Resuming Session")
	p.KeyValue([][]string{
		{"Session", sessionID},
		{"Objective", objective},
		{"Adapter", cfg.Workers.DefaultAdapter},
		{"Workers", fmt.Sprintf("%d max parallel", cfg.Workers.MaxParallel)},
	})
	p.Println("")

	// Create queen instance
	q, err := queen.New(cfg, logger)
	if err != nil {
		return fmt.Errorf("init queen: %w", err)
	}
	defer q.Close()

	q.SetPrinter(p)

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
	defer signal.Stop(sigs)

	go func() {
		<-sigs
		p.Warning("Received shutdown signal, gracefully stopping...")
		cancel()
	}()

	// Run with the resumed session, preferring agent mode
	var runErr error
	if !forceLegacy && q.SupportsAgentMode() {
		p.Success("Agent mode: resuming with tool-using Queen")
		runErr = q.RunAgentResume(runCtx, sessionID)
	} else {
		runErr = q.Run(runCtx, objective)
	}
	if runErr != nil {
		return fmt.Errorf("queen failed: %w", runErr)
	}

	p.Println("")
	p.Header("Mission Complete")
	return nil
}

func cmdKill(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args().Slice()
	if len(args) != 1 {
		return fmt.Errorf("usage: waggle kill <session-id>")
	}

	sessionID := args[0]
	projectDir := cmd.String("project")

	hiveDir := filepath.Join(projectDir, ".hive")
	dbPath := filepath.Join(hiveDir, "hive.db")

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("no sessions found. Run 'waggle run <objective>' first")
	}

	db, err := state.OpenDB(hiveDir)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	session, err := db.GetSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	if session.Status == "stopped" || session.Status == "done" {
		return fmt.Errorf("session %s is already %s", sessionID, session.Status)
	}

	if err := db.StopSession(sessionID); err != nil {
		return fmt.Errorf("stop session: %w", err)
	}

	output.NewPrinter(output.ModePlain, false).Success("Session %s stopped", sessionID)
	return nil
}
