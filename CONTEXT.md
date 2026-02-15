# Waggle — Project Context

> Last updated: 2026-02-15

## What This Is

A multi-agent orchestration framework in Go. A central **Queen** agent decomposes objectives into tasks, delegates them to **Worker Bee** sub-agents running via coding CLI tools, monitors execution, reviews results with LLM judgment, and reports findings back to the user.

Think of it as a task runner where the tasks are executed by AI coding agents in parallel, with an AI reviewer ensuring quality.

## Architecture

```
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
   │  │ 👑 Queen Panel        │   │  Real-time LLM reasoning,
   │  │  thinking / tools     │   │  tool calls, results
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
The structured Plan → Delegate → Monitor → Review → Replan loop. The Queen's LLM is called at specific phases (planning, review, replan) with structured prompts.

## Module Map

| Package | File(s) | Purpose |
|---------|---------|--------|
| `cmd/waggle` | `main.go`, `app.go`, `commands.go`, `status.go`, `tasks.go` | CLI entry point (urfave/cli): `run`, `init`, `status`, `config`, `resume` |
| `internal/queen` | `queen.go` | **Core orchestrator** — legacy Plan/Delegate/Monitor/Review/Replan loop |
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
| `internal/tui` | `model.go`, `view.go`, `styles.go`, `events.go`, `bridge.go` | **Bubble Tea TUI dashboard** — real-time Queen/task/worker display |
| `internal/worker` | `worker.go` | `Bee` interface + concurrent `Pool` with limits |
| `internal/adapter` | 6 adapters + utils | CLI wrappers: claude, codex, opencode, kimi, gemini, exec |
| `internal/adapter` | `adapter.go` | `Registry` + `TaskRouter` (maps task types → configured default adapter) |
| `internal/bus` | `bus.go` | In-process pub/sub message bus with panic-safe handler dispatch |
| `internal/blackboard` | `blackboard.go` | Shared memory — workers post results, Queen reads (deep-copy on History) |
| `internal/state` | `db.go` | **SQLite persistence** — sessions, events, tasks, blackboard, kv |
| `internal/task` | `task.go` | Task graph with dependency tracking, priority, status, **cycle detection** |
| `internal/config` | `config.go` | Configuration with defaults, JSON serialization |
| `internal/safety` | `safety.go` | Path allowlisting, command blocklisting — **enforced in all adapters** |
| `internal/compact` | `compact.go` | Context window management, token estimation, summarization |
| `internal/errors` | `errors.go` | Error classification, retry/permanent types, backoff |

**Total: ~8800 lines of source across 33 Go files + ~7500 lines of tests**

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
    Output() string
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
|------|---------|
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

Bubble Tea-based terminal UI with three panels:
- **Queen Panel** — Real-time display of Queen's thinking, tool calls, and results. Scrollable with j/k or arrow keys.
- **Task Panel** — Task list with status icons (⏳ pending, 🔄 running, ✅ complete, ❌ failed), worker assignments.
- **Status Bar** — Elapsed time, active worker count, completion status.

The TUI is automatically used when stdout is a TTY. Falls back to plain log output with `--plain` flag. After completion, waits for user keypress before exiting.

The bridge (`tui/bridge.go`) routes log output from the Queen into structured TUI messages, with message buffering for events that arrive before the TUI starts.

## Provider Selection

Configured via `waggle.json`:
```json
{"queen": {"provider": "anthropic"}}   // Anthropic API (tool-use, needs ANTHROPIC_API_KEY)
{"queen": {"provider": "openai"}}      // OpenAI API (tool-use, needs OPENAI_API_KEY)
{"queen": {"provider": "gemini"}}      // Gemini API (tool-use, needs GEMINI_API_KEY)
{"queen": {"provider": "codex"}}       // Codex CLI (tool-use via OpenAI-compatible API)
{"queen": {"provider": "kimi"}}        // Kimi CLI (no tool-use, legacy mode)
{"queen": {"provider": "claude-cli"}}  // Claude CLI (no tool-use, legacy mode)
{"queen": {"provider": "opencode"}}    // OpenCode CLI (no tool-use, legacy mode)
```
No provider = review/replan disabled, legacy exit-code-based behavior.

## Task Router

`TaskRouter` maps task types to adapters. It initializes all routes from `workers.default_adapter` in the config. The Queen's `assign_task` tool uses the router to select which adapter spawns the worker for each task.

## Scope Constraints System

Three layers control what workers can and cannot do:

1. **Plan prompt** — The Queen instructs the planner to produce narrowly-scoped tasks with `constraints` (what NOT to do) and `allowed_paths` (what files may be touched).
2. **Default constraints** — Every task gets baseline rules injected at delegation: no out-of-scope changes, no unsolicited refactoring, no signature changes, report issues don't fix them.
3. **Worker prompt** — `buildPrompt()` renders a `--- SCOPE CONSTRAINTS ---` block visible to every worker, combining planner-generated and default constraints.

## Safety Guard

The `safety.Guard` is wired into all adapter constructors and enforced at spawn time:
- `ValidateTaskPaths()` — checks task's allowed_paths against the guard's allowlist
- `CheckCommand()` — scans task description/script for blocked commands
- `IsReadOnly()` — prepends read-only warning to worker prompts when enabled
- All adapter goroutines have `defer/recover` for panic safety

## Persistence Layer

```
.hive/
└── hive.db       # SQLite (WAL mode) — sole persistence store
```

### SQLite Schema
- **sessions** — one row per `waggle run` invocation (id, objective, status, phase, iteration, timestamps)
- **events** — append-only event log indexed by session + type
- **tasks** — full task state (status, worker_id, result JSON, result_data, retries, deps)
- **blackboard** — persisted shared memory (key/value per session)
- **kv** — general purpose key-value store (used for persisting agent conversation turns)

## CLI Commands

```bash
waggle init                          # Create .hive/ and waggle.json
waggle run "<objective>"              # Run with AI planning (TUI if TTY)
waggle --adapter kimi run "<obj>"     # Specify worker adapter
waggle --adapter exec --tasks f.json run "<obj>"  # Pre-defined tasks
waggle --workers 8 run "<obj>"        # Set parallelism
waggle --plain run "<obj>"            # Force plain log output (no TUI)
waggle --legacy run "<obj>"           # Force legacy orchestration loop
waggle status                         # Show current/last session
waggle config                         # Show configuration
waggle resume <session-id>            # Resume interrupted session
```

## Configuration (`waggle.json`)

```json
{
  "queen": {
    "provider": "codex",
    "model": "gpt-5-nano-2025-08-07",
    "max_iterations": 50,
    "plan_timeout": 300000000000,
    "review_timeout": 120000000000,
    "compact_after_messages": 100
  },
  "workers": {
    "max_parallel": 4,
    "default_timeout": 600000000000,
    "max_retries": 2,
    "default_adapter": "kimi"
  },
  "adapters": {
    "kimi": { "command": "kimi", "args": ["--print", "--final-message-only", "-p"] },
    "opencode": { "command": "opencode", "args": ["run"] },
    "gemini": { "command": "gemini" },
    "claude-code": { "command": "claude", "args": ["-p"] },
    "codex": { "command": "codex", "args": ["exec"] },
    "exec": { "command": "bash" }
  },
  "safety": {
    "allowed_paths": ["."],
    "blocked_commands": ["rm -rf /", "sudo rm"],
    "max_file_size": 10485760
  }
}
```

## Adapters — Current State

| Adapter | CLI | Non-interactive Command | Status |
|---------|-----|------------------------|--------|
| `kimi` | Kimi Code | `kimi --print --final-message-only -p "<prompt>"` | ✅ **Working, fast (~60s/task)** |
| `opencode` | OpenCode | `opencode run "<prompt>"` | ✅ Working, slow (~2-3min/task) |
| `gemini` | Gemini CLI | `echo "<prompt>" \| gemini` | 🔑 Installed, needs capacity |
| `claude-code` | Claude Code | `claude -p "<prompt>"` | 🔑 Needs `/login` |
| `codex` | Codex | `codex exec "<prompt>"` | ✅ Working |
| `exec` | bash | `bash -c "<description>"` | ✅ Always available |

## What Was Tested End-to-End

1. **exec adapter** — parallel shell tasks with dependencies (4 tasks, 2 waves) ✅
2. **opencode adapter** — 15-task code review, 3 waves, 12/15 completed before timeout ✅
3. **kimi adapter** — 5-task codebase review, 2 waves, all completed in ~3 minutes ✅
4. **Pre-defined tasks** (`--tasks file.json`) — loaded and executed with dependency ordering ✅
5. **Retry logic** — failed tasks retried up to `max_retries`, then marked failed ✅
6. **Status command** — SQLite-backed with JSONL fallback for legacy sessions ✅
7. **Real-time output** — worker findings printed as they complete ✅
8. **Final report** — complete consolidated report at end of run ✅
9. **Scope constraints** — kimi workers stayed in `internal/bus/` (1 file) vs. 19 files without constraints ✅
10. **Session status preservation** — Close() preserves 'done'/'failed' status, 3 unit tests ✅
11. **Bus panic recovery** — panicking handlers caught, other handlers still execute, 5 tests ✅
12. **LLM review + replan** — kimi as Queen's brain, approved 4 tasks, replan returned 0 new tasks ✅
13. **Cycle detection** — DFS-based, 12 test cases, integrated into parsePlanOutput() ✅
14. **Agent mode** — Queen as autonomous tool-using agent with Anthropic/OpenAI/Gemini providers ✅
15. **TUI dashboard** — Real-time Bubble Tea display of Queen reasoning, tasks, workers ✅
16. **Session resume** — Agent-mode conversation restored from SQLite kv store ✅

## Known Issues

- None currently — all tests passing.

## Dependencies

- `modernc.org/sqlite` — pure Go SQLite driver (no CGO)
- `github.com/anthropics/anthropic-sdk-go` — Anthropic API client
- `github.com/urfave/cli/v3` — CLI framework
- `github.com/charmbracelet/bubbletea` — TUI framework
- `github.com/charmbracelet/lipgloss` — TUI styling
- `golang.org/x/term` — TTY detection
- Go 1.26+

## Repository

- GitHub: https://github.com/HexSleeves/waggle
- 41 commits on `main`
- No branches, no CI, no releases
