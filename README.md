# 🐝 Queen Bee — Agent Orchestration System

A multi-agent orchestration framework where a central **Queen** agent manages, delegates to, and synthesizes the work of multiple **Worker Bee** sub-agents. Designed to integrate with coding agent CLIs (Claude Code, Codex, OpenCode) as execution runtimes.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                      USER OBJECTIVE                         │
└──────────────────────────────┬──────────────────────────────┘
                               │
                    ┌──────────┴──────────┐
                    │    👑 QUEEN BEE     │
                    │                     │
                    │  Plan → Delegate    │
                    │    ↓         ↓       │
                    │  Review ← Monitor   │
                    └───┬─────┬─────┬─────┘
                        │     │     │
              ┌────────┘     │     └────────┐
              │               │              │
     ┌────────┴───┐  ┌─────┴──────┐ ┌────┴───────┐
     │ 🐝 CoderBee  │  │ 🐝 Research │ │ 🐝 TesterBee│
     │ (claude)   │  │    Bee     │ │ (codex)    │
     └────────────┘  └────────────┘ └────────────┘
              │               │              │
     ┌────────┴───────────┴─────────────┴───────┐
     │           📋 BLACKBOARD (shared memory)          │
     └─────────────────────────────────────────────┘
```

## Design Principles

- **Hierarchical Orchestration** — Queen is the single source of truth for state, planning, and delegation
- **Event-Driven State** — Append-only JSONL event log for durability and replayability
- **Model Agnostic** — Core orchestrator works with any reasoning model via CLI adapters
- **Context Compaction** — Automatic summarization keeps the Queen effective in long sessions
- **Safety First** — Workers are path-restricted and command-filtered

## Quick Start

```bash
# Build
go build -o queen-bee ./cmd/queen-bee/

# Initialize a hive in your project
cd /path/to/your/project
queen-bee init

# Run with an objective (uses AI adapter for planning + execution)
queen-bee run "Refactor the auth module to use JWT tokens"

# With options
queen-bee --adapter codex --workers 8 run "Add unit tests for all handlers"

# Use exec adapter for shell commands (no AI CLI required)
queen-bee --adapter exec run "go test ./..."

# Pre-defined task file for parallel execution
queen-bee --adapter exec --tasks tasks.json -v run "Run analysis"

# Verbose mode shows task results at completion
queen-bee --adapter exec -v run "ls -la"
```

### Task File Format

Pre-define parallel tasks with dependencies in JSON:

```json
[
  {
    "id": "lint",
    "type": "test",
    "title": "Run linter",
    "description": "golangci-lint run ./...",
    "priority": 2
  },
  {
    "id": "test",
    "type": "test",
    "title": "Run tests",
    "description": "go test ./...",
    "priority": 3,
    "depends_on": ["lint"]
  }
]
```

## Project Structure

```
queen-bee/
├── cmd/queen-bee/          # CLI entry point
│   └── main.go
├── internal/
│   ├── queen/              # 👑 The Queen — core orchestrator loop
│   │   └── queen.go        #    Plan → Delegate → Monitor → Review
│   ├── worker/             # 🐝 Worker Bee interface and pool manager
│   │   └── worker.go
│   ├── adapter/            # CLI wrapper adapters
│   │   ├── adapter.go      #    Registry + Task Router
│   │   ├── claude.go       #    Claude Code (-p flag)
│   │   ├── codex.go        #    Codex (exec subcommand)
│   │   ├── opencode.go     #    OpenCode CLI
│   │   ├── shelley.go      #    Shelley CLI
│   │   └── exec.go         #    Direct shell execution (bash -c)
│   ├── bus/                # 📨 In-process pub/sub message bus
│   │   └── bus.go
│   ├── blackboard/         # 📋 Shared memory for inter-agent comms
│   │   └── blackboard.go
│   ├── state/              # 💾 Append-only event log + state snapshots
│   │   └── state.go
│   ├── task/               # 📌 Task graph with dependency tracking
│   │   └── task.go
│   ├── config/             # ⚙️  Configuration management
│   │   └── config.go
│   ├── safety/             # 🛡️  Path restriction and command filtering
│   │   └── safety.go
│   └── compact/            # 📦 Context window compaction
│       └── compact.go
├── go.mod
├── .gitignore
└── README.md
```

## Core Modules

### Queen (`internal/queen`)
The central coordinator running a **Plan → Delegate → Monitor → Review** loop:

1. **Plan** — Decomposes the objective into a task graph via an LLM call
2. **Delegate** — Assigns ready tasks (respecting dependencies) to worker bees in parallel
3. **Monitor** — Polls workers until all complete or timeout
4. **Review** — Evaluates results, retries failures, decides if more work is needed

The loop continues until all tasks are complete or max iterations are reached.

### Workers (`internal/worker`)
Ephemeral, stateless execution units implementing the `Bee` interface:

```go
type Bee interface {
    ID() string
    Type() string
    Spawn(ctx context.Context, t *task.Task) error
    Monitor() Status
    Result() *task.Result
    Kill() error
    Output() string
}
```

Managed by a `Pool` that enforces concurrency limits.

### Adapters (`internal/adapter`)
Wrap coding agent CLIs behind a uniform interface:

| Adapter | CLI | Command |
|---------|-----|---------|
| `claude-code` | Claude Code | `claude -p "<prompt>"` |
| `codex` | Codex | `codex exec "<prompt>"` |
| `opencode` | OpenCode | `opencode run "<prompt>"` |
| `kimi` | Kimi Code | `kimi --print --final-message-only -p "<prompt>"` |
| `gemini` | Gemini CLI | `echo "<prompt>" \| gemini` |
| `exec` | Shell (bash) | `bash -c "<description>"` |
| `shelley` | Shelley | `shelley -p "<prompt>"` |

The **Task Router** maps task types to adapters (e.g., code tasks → claude-code, test tasks → codex).

### Message Bus (`internal/bus`)
In-process pub/sub for decoupled event handling. Event types:
- `task.created`, `task.status_changed`, `task.assigned`
- `worker.spawned`, `worker.completed`, `worker.failed`
- `blackboard.update`, `queen.decision`, `queen.plan`

### Blackboard (`internal/blackboard`)
Shared memory space where workers post partial results:
- Queen reads to build context for decisions
- Supports querying by key, tag, worker, or task
- Includes a `Summarize()` method for context compaction

### State (`internal/state`)
Durable persistence layer:
- **`log.jsonl`** — Append-only event log (every bus message is recorded)
- **`state.json`** — Snapshot of current state for quick resume
- Enables session resume after interruption

### Context Compaction (`internal/compact`)
Manages the Queen's context window:
- Estimates token usage (~4 chars/token)
- Auto-compacts at 75% capacity
- Keeps recent 25% of messages, summarizes the rest
- Pluggable summarizer (default: extractive; can be LLM-backed)

### Safety (`internal/safety`)
Enforces worker boundaries:
- Path allowlisting (workers can only access specified directories)
- Command blocklisting (prevents dangerous commands)
- File size limits
- Read-only mode option

## Configuration

`queen.json` (created by `queen-bee init`):

```json
{
  "project_dir": ".",
  "hive_dir": ".hive",
  "queen": {
    "model": "claude-sonnet-4-20250514",
    "provider": "anthropic",
    "max_iterations": 50,
    "compact_after_messages": 100
  },
  "workers": {
    "max_parallel": 4,
    "default_timeout": "10m",
    "max_retries": 2,
    "default_adapter": "claude-code"
  },
  "adapters": {
    "claude-code": { "command": "claude", "args": ["--print"] },
    "codex": { "command": "codex", "args": ["exec"] },
    "opencode": { "command": "opencode" }
  },
  "safety": {
    "allowed_paths": ["."],
    "blocked_commands": ["rm -rf /", "sudo rm"],
    "max_file_size": 10485760
  }
}
```

## Persistence & Resume

All state is stored in `.hive/`:
```
.hive/
├── log.jsonl    # Every event, append-only
└── state.json   # Current snapshot
```

Interrupted sessions can be resumed:
```bash
queen-bee resume
```

## Extending

### Add a New Adapter

1. Create `internal/adapter/myagent.go`
2. Implement the `Adapter` interface (Name, Available, CreateWorker)
3. Implement the `worker.Bee` interface on the worker struct
4. Register in `queen.New()`

### Custom Task Types

Add new types in `internal/task/task.go` and configure routing in `internal/adapter/adapter.go`.

### LLM-Backed Summarizer

Replace `compact.DefaultSummarizer` with a function that calls your preferred LLM API.

## License

MIT
