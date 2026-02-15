# Waggle — Project Context

> Last updated: 2026-02-15

## What This Is

A multi-agent orchestration framework in Go. A central **Queen** agent decomposes objectives into tasks, delegates them to **Worker Bee** sub-agents running via coding CLI tools, monitors execution, reviews results with LLM judgment, and reports findings back to the user.

Think of it as a task runner where the tasks are executed by AI coding agents in parallel, with an AI reviewer ensuring quality.

## Architecture

```bash
User Objective
       │
   ┌───▼───┐
   │ Queen │  Autonomous tool-using LLM agent (agent mode)
   │       │  OR Plan → Delegate → Monitor → Review → Replan (legacy mode)
   └───┬───┘
       │ spawns via adapters (with safety guard + scope constraints)
   ┌───┴────────────┬──────────────┐
   ▼                ▼              ▼
┌──────┐      ┌──────┐      ┌──────┐
│Worker│      │Worker│      │Worker│   (parallel, ephemeral)
│(kimi)│      │(kimi)│      │(exec)│
└──┬───┘      └──┬───┘      └──┬───┘
   │              │              │
   └──────────────┴──────────────┘
                  │
            ┌─────▼─────┐
            │ Blackboard │  shared results
            │  SQLite DB │  persistent state
            │  Event Log │  append-only audit
            └───────────┘

   ┌─────────────────────────────┐
   │      TUI Dashboard          │
   │  ┌──────────────────────┐   │
   │  │ 👑 Queen / ⚙ Worker  │   │  Tab to switch panels,
   │  │  live streaming       │   │  real-time output
   │  ├──────────────────────┤   │
   │  │ 📋 Task Panel         │   │  Task status, worker
   │  │  status / workers     │   │  assignments, progress
   │  ├──────────────────────┤   │
   │  │ 🐝 Status Bar         │   │  Elapsed, worker count
   │  └──────────────────────┘   │
   └─────────────────────────────┘
```

## Two Execution Modes

### Agent Mode (default when provider supports tools)

The Queen runs as an autonomous tool-using LLM agent. She receives the objective, and the Go code just executes tool calls and feeds results back. The Queen decides what tools to call and when: `create_tasks`, `assign_task`, `wait_for_workers`, `get_task_output`, `approve_task`, `reject_task`, `read_file`, `list_files`, `complete`, `fail`.

### Legacy Mode (fallback / `--legacy` flag)

The structured Plan → Delegate → Monitor → Review → Replan loop. The Queen's LLM is called at specific phases (planning, review, replan) with structured prompts. After review, skips back to Delegate if ready tasks exist (avoids unnecessary re-planning).

## Module Map

| Package | File(s) | Purpose |
|---------|---------|--------|
| `cmd/waggle` | `main.go`, `app.go`, `commands.go`, `status.go`, `tasks.go` | CLI entry point (urfave/cli): `run`, `init`, `status`, `config`, `resume` |
| `internal/queen` | `queen.go` | **Core orchestrator** — main loop, initialization, logging |
| `internal/queen` | `delegate.go` | Legacy delegation phase — assigns ready tasks to workers |
| `internal/queen` | `planner.go` | Legacy planning phase — LLM-backed task decomposition + parsing |
| `internal/queen` | `failure.go` | Task failure handling with error classification + retry backoff |
| `internal/queen` | `reporter.go` | Completion reporting — task result formatting + summary |
| `internal/queen` | `agent.go` | **Agent mode** — autonomous tool-using LLM loop with conversation history |
| `internal/queen` | `tools.go` | Tool definitions + handlers (create_tasks, assign_task, wait, approve, etc.) |
| `internal/queen` | `prompt.go` | System prompt builder for agent mode |
| `internal/queen` | `review.go` | LLM-backed review: evaluates worker output quality |
| `internal/queen` | `replan.go` | LLM-backed replan: identifies follow-up tasks |
| `internal/llm` | `client.go`, `types.go` | **Provider-agnostic LLM client** + `ToolClient` interface |
| `internal/llm` | `anthropic.go` | Anthropic API client with tool-use |
| `internal/llm` | `openai.go` | OpenAI-compatible API client with tool-use |
| `internal/llm` | `gemini.go` | Google Gemini API client with tool-use |
| `internal/llm` | `cli.go` | CLI-based LLM wrapper (no tool support) |
| `internal/llm` | `factory.go` | Provider factory: anthropic, openai, gemini, codex, kimi, gemini-cli, claude-cli, opencode |
| `internal/tui` | `model.go`, `view.go`, `styles.go`, `events.go`, `bridge.go` | **Bubble Tea TUI dashboard** — Queen/worker/task panels with live streaming |
| `internal/worker` | `worker.go` | `Bee` interface + concurrent `Pool` with per-task timeout enforcement |
| `internal/adapter` | `generic.go` | **`CLIAdapter` + `CLIWorker`** — shared base for all CLI adapters with 3 prompt modes |
| `internal/adapter` | `claude.go`, `kimi.go`, `codex.go`, `opencode.go`, `gemini.go`, `exec.go` | Thin constructors (23-29 lines each) configuring `CLIAdapter` |
| `internal/adapter` | `adapter.go` | `Registry` + `TaskRouter` (maps task types → configured default adapter) |
| `internal/adapter` | `utils.go` | `streamWriter` (live output with max size cap), `buildPrompt()`, `getExitCode()` |
| `internal/bus` | `bus.go` | In-process pub/sub message bus with panic-safe handler dispatch |
| `internal/blackboard` | `blackboard.go` | Shared memory — workers post results, Queen reads. History capped at 10k entries. |
| `internal/state` | `db.go` | **SQLite persistence** — sessions, events, tasks, blackboard, kv |
| `internal/task` | `task.go` | Task graph with dependency tracking, priority, status, cycle detection, `RetryAfter` backoff |
| `internal/config` | `config.go` | Configuration with defaults, JSON serialization |
| `internal/safety` | `safety.go` | Path allowlisting, command blocklisting — enforced in all adapters |
| `internal/compact` | `compact.go` | Context window management, token estimation, summarization |
| `internal/errors` | `errors.go` | Error classification, retry/permanent types, jittered exponential backoff |

**Total: ~9,600 lines of source + ~12,600 lines of tests across 22,200 total Go lines (64 commits)**

## Key Interfaces

### `worker.Bee` — What every worker must implement

```go
type Bee interface {
    ID() string
    Type() string
    Spawn(ctx context.Context, t *task.Task) error
    Monitor() Status  // idle, running, stuck, complete, failed
    Result() *task.Result
    Kill() error
    Output() string   // Returns live streaming output during execution
}
```

### `adapter.Adapter` — How CLIs are wrapped

```go
type Adapter interface {
    Name() string
    Available() bool
    CreateWorker(id string) worker.Bee
}
```

All 6 adapters share `CLIAdapter` + `CLIWorker` from `generic.go`. Three `PromptMode` options:
- `PromptAsArg` — append prompt as last CLI argument (claude, kimi, codex, opencode)
- `PromptOnStdin` — pipe prompt to stdin (gemini)
- `PromptAsScript` — run task description as `bash -c` script (exec)

### `llm.Client` — Provider-agnostic LLM interface

```go
type Client interface {
    Chat(ctx context.Context, systemPrompt, userMessage string) (string, error)
    ChatWithHistory(ctx context.Context, systemPrompt string, messages []Message) (string, error)
}
```

### `llm.ToolClient` — LLM with tool-use support (extends Client)

```go
type ToolClient interface {
    Client
    ChatWithTools(ctx context.Context, system string, messages []ToolMessage, tools []ToolDef) (*Response, error)
}
```

Implementations: `AnthropicClient`, `OpenAIClient`, `GeminiClient` (all tool-capable), `CLIClient` (no tools, triggers legacy mode).

## Queen's Agent Tools

| Tool | Purpose |
|------|--------|
| `create_tasks` | Create tasks in the task graph with types, priorities, dependencies, constraints |
| `assign_task` | Assign a pending task to a worker (respects deps, pool capacity, configured adapter) |
| `wait_for_workers` | Block until one or more workers complete (with timeout) |
| `get_status` | Get current status of all tasks |
| `get_task_output` | Read a completed/failed task's output |
| `approve_task` | Mark a completed task as approved |
| `reject_task` | Reject a task with feedback, re-queue for retry |
| `read_file` | Read a file from the project (safety-checked) |
| `list_files` | List directory contents |
| `complete` | Declare the objective complete with summary |
| `fail` | Declare the objective failed with reason |

## TUI Dashboard

Bubble Tea-based terminal UI with switchable panels:

- **Queen Panel** — Real-time display of Queen's thinking, tool calls, and results. Scrollable (j/k, arrows). Scroll clamped so content is always visible.
- **Worker Panels** — Live streaming output from each active worker. Tab/Shift+Tab to cycle, ←→ to navigate, 0 to return to Queen.
- **Task Panel** — Task list with status icons (⏳ pending, 🔄 running, ✅ complete, ❌ failed), worker assignments.
- **Status Bar** — Elapsed time, active worker count, navigation hints.

The TUI auto-detects TTY. Falls back to plain log output with `--plain`. After completion, waits for user keypress before exiting. Interactive mode (no args) shows an objective prompt.

### Output Streaming

All adapters use `streamWriter` (thread-safe `io.MultiWriter` tee) to write process stdout/stderr to `w.output` in real-time. Output capped at `workers.max_output_size` (default 1MB) — truncation marker appended when exceeded. The TUI polling goroutine sends `WorkerOutputMsg` every 500ms.

### Bridge

The bridge (`tui/bridge.go`) routes log output from the Queen into structured TUI messages, with message buffering for events that arrive before the TUI starts. Supports quiet mode (`NewQuietProgram()`).

## Task Execution Model

### Parallelism
- **Planning prompt** tells the LLM planner the worker count and instructs it to minimize dependencies
- **Agent mode prompt** instructs the Queen to assign ALL ready tasks before waiting
- **Legacy mode** Review→Delegate shortcut: when review finds ready tasks, skips back to delegation
- **Worker pool** enforces `max_parallel` limit; `assign_task` returns error when full

### Per-Worker Timeout
- `Pool.Spawn` wraps context with `context.WithTimeout(ctx, task.Timeout)` when `Timeout > 0`
- `exec.CommandContext` kills the process when the deadline expires
- `CLIWorker` detects `context.DeadlineExceeded` and reports `[timeout] worker killed`
- Bus event `MsgWorkerFailed` published on timeout
- Default timeout: 10 minutes (from `workers.default_timeout`)

### Task Retry with Backoff
- Failed tasks get `RetryAfter` set using jittered exponential backoff
- `TaskGraph.Ready()` skips tasks whose `RetryAfter` hasn't elapsed
- Max retries configurable per-task and globally via `workers.max_retries`

## Provider Selection

Configured via `waggle.json`:

```json
{"queen": {"provider": "anthropic"}}   // Anthropic API (tool-use, needs ANTHROPIC_API_KEY)
{"queen": {"provider": "openai"}}      // OpenAI API (tool-use, needs OPENAI_API_KEY)
{"queen": {"provider": "gemini-api"}}  // Gemini API (tool-use, needs GEMINI_API_KEY)
{"queen": {"provider": "codex"}}       // Codex (tool-use via OpenAI-compatible API)
{"queen": {"provider": "kimi"}}        // Kimi CLI (no tool-use, legacy mode)
{"queen": {"provider": "claude-cli"}}  // Claude CLI (no tool-use, legacy mode)
{"queen": {"provider": "opencode"}}    // OpenCode CLI (no tool-use, legacy mode)
```

## Scope Constraints System

Three layers control what workers can and cannot do:

1. **Plan prompt** — narrowly-scoped tasks with `constraints` and `allowed_paths`
2. **Default constraints** — injected via `injectDefaultConstraints()` at delegation: no out-of-scope changes, no unsolicited refactoring, no signature changes
3. **Worker prompt** — `buildPrompt()` renders `--- SCOPE CONSTRAINTS ---` block

## Safety Guard

`safety.Guard` wired into all adapter constructors, enforced at spawn time:
- `ValidateTaskPaths()`, `CheckCommand()`, `IsReadOnly()`, `CheckFileSize()`
- All adapter goroutines have `defer/recover` for panic safety

## Persistence Layer

```
.hive/
└── hive.db       # SQLite (WAL mode) — sole persistence store
```

### SQLite Schema
- **sessions** — one row per `waggle run` invocation
- **events** — append-only event log indexed by session + type
- **tasks** — full task state (status, worker_id, result JSON, retries, deps)
- **blackboard** — persisted shared memory (key/value per session)
- **kv** — general purpose key-value store (agent conversation turns)

## CLI Commands

```bash
waggle init                          # Create .hive/ and waggle.json
waggle run "<objective>"              # Run with AI planning (TUI if TTY)
waggle                               # Interactive TUI mode (prompts for objective)
waggle --adapter kimi run "<obj>"     # Specify worker adapter
waggle --adapter exec --tasks f.json run "<obj>"  # Pre-defined tasks
waggle --workers 8 run "<obj>"        # Set parallelism
waggle --plain run "<obj>"            # Force plain log output (no TUI)
waggle --legacy run "<obj>"           # Force legacy orchestration loop
waggle --quiet run "<obj>"            # Suppress all output except errors
waggle --json run "<obj>"             # Output results as JSON
waggle status                         # Show current/last session
waggle config                         # Show configuration
waggle resume <session-id>            # Resume interrupted session
```

## Build & Development

```bash
just build              # Build ./waggle binary
just test               # Run all tests
just test-pkg queen     # Test specific package
just test-race          # Tests with race detector
just ci                 # fmt-check + vet + test
just run "<obj>"        # Build & run with objective
just run-interactive    # Launch TUI prompt
just fmt                # Format all Go files
just clean              # Remove binary + .hive/
```

## Configuration (`waggle.json`)

```json
{
  "queen": {
    "provider": "anthropic",
    "model": "claude-sonnet-4-20250514",
    "max_iterations": 50,
    "plan_timeout": 300000000000,
    "review_timeout": 120000000000,
    "compact_after_messages": 100
  },
  "workers": {
    "max_parallel": 4,
    "default_timeout": 600000000000,
    "max_retries": 2,
    "default_adapter": "claude-code",
    "max_output_size": 1048576
  },
  "adapters": { ... },
  "safety": {
    "allowed_paths": ["."],
    "blocked_commands": ["rm -rf /", "sudo rm"],
    "max_file_size": 10485760
  }
}
```

## Adapters — Current State

| Adapter | CLI | Status |
|---------|-----|--------|
| `kimi` | `kimi --print --final-message-only -p "<prompt>"` | ✅ Working (rate-limited on this VM) |
| `opencode` | `opencode run "<prompt>"` | ✅ Working |
| `gemini` | `echo "<prompt>" \| gemini` | 🔑 Needs capacity |
| `claude-code` | `claude -p "<prompt>"` | 🔑 Needs `/login` on this VM |
| `codex` | `codex exec "<prompt>"` | ✅ Working |
| `exec` | `bash -c "<description>"` | ✅ Always available |

**Note**: On this VM, kimi is rate-limited and claude-code needs login. No API keys are set for Anthropic/OpenAI/Gemini. The exec adapter always works for testing.

## Test Coverage

| Package | Tests | Status |
|---------|-------|--------|
| `adapter` | Functionality, safety integration, prompt building, stream writer | ✅ |
| `blackboard` | Post/Read, List, Delete, History, Watch, concurrency | ✅ |
| `bus` | Publish, Subscribe, panic recovery | ✅ |
| `compact` | Context lifecycle, compaction, token estimation, summarizer | ✅ |
| `config` | Defaults, Load/Save roundtrip, HivePath, output modes | ✅ |
| `errors` | Classification, backoff, jitter, panic recovery | ✅ |
| `llm` | Provider types, tool definitions | ✅ |
| `queen` | Agent mode, tools (11), orchestrator loop, review, replan, prompts | ✅ |
| `safety` | Guard creation, path/command/filesize checks, task validation | ✅ |
| `state` | SQLite CRUD, sessions, tasks, events, kv | ✅ |
| `task` | Graph, dependencies, cycle detection, status, ready | ✅ |
| `worker` | Pool lifecycle, spawn, timeout, kill, concurrency | ✅ |
| `cmd/waggle` | ❌ No tests |
| `output` | ❌ No tests |
| `tui` | ❌ No tests |

**12,600 lines of tests across 30 test files. All passing.**

## What Was Tested End-to-End

1. **exec adapter** — parallel shell tasks with dependencies (4 tasks, 2 waves) ✅
2. **opencode adapter** — 15-task code review, 3 waves, 12/15 completed ✅
3. **kimi adapter** — 5-task codebase review, 2 waves, all completed ~3min ✅
4. **Pre-defined tasks** (`--tasks file.json`) with dependency ordering ✅
5. **Scope constraints** — workers stayed in allowed paths ✅
6. **LLM review + replan** — approved tasks, replan returned 0 new tasks ✅
7. **Agent mode** — Queen as autonomous tool-using agent ✅
8. **TUI dashboard** — real-time Queen/worker/task display ✅
9. **Waggle on itself** — framework planned 5 tasks, delegated in parallel waves of 4 ✅

## Known Issues

- On this VM: kimi rate-limited, claude-code needs `/login`, no API keys set
- CI workflow needs PAT with `workflow` scope to push (was pushed by user)
- Disk space can run low (~19GB total) — run `go clean -cache` if needed

## Dependencies

- `modernc.org/sqlite` — pure Go SQLite driver (no CGO)
- `github.com/anthropics/anthropic-sdk-go` — Anthropic API client
- `github.com/urfave/cli/v3` — CLI framework
- `github.com/charmbracelet/bubbletea` — TUI framework
- `github.com/charmbracelet/lipgloss` — TUI styling
- `golang.org/x/term` — TTY detection
- Go 1.26+

## Repository

- GitHub: <https://github.com/HexSleeves/waggle>
- 64 commits on `main`
- Build: `just build` / `just ci`
- CI: GitHub Actions (fmt-check + vet + test + build on push/PR)
- No releases yet
