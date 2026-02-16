<p align="center">
  <img src="assets/banner.svg" alt="Waggle" width="800">
</p>

<p align="center">
  <strong>Multi-agent orchestration through the waggle dance.</strong>
</p>

<p align="center">
  <a href="#installation">Installation</a> •
  <a href="#quick-start">Quick Start</a> •
  <a href="#architecture">Architecture</a> •
  <a href="#configuration">Configuration</a> •
  <a href="#documentation">Documentation</a>
</p>

---

## What is Waggle?

Waggle is a multi-agent orchestration framework written in Go. A central **Queen** agent — powered by
any LLM with tool-use support — decomposes complex objectives into a graph of tasks, delegates them
to **Worker Bee** sub-agents (AI coding CLIs like Claude Code, Kimi, Codex, Gemini, OpenCode, or plain
shell commands), monitors execution in parallel, reviews results, and replans when needed.

The Queen runs as an **autonomous tool-using LLM agent**: she receives an objective, decides what
tasks to create, assigns them to workers, waits for results, reviews output, and declares completion
— all through tool calls. The Go code just executes tools and feeds results back.

The entire lifecycle is modeled after the [waggle dance](https://en.wikipedia.org/wiki/Waggle_dance),
the figure-eight dance honeybees use to communicate exactly where resources are and how to get them.

## How it Works

In **agent mode** (default), the Queen is an autonomous LLM that orchestrates everything through tool calls:

| Step | What Happens |
| ---- | ------------ |
| 🎯 Receive objective | Queen gets the user's goal and project context |
| 📋 Create tasks | Queen calls `create_tasks` to build a dependency graph |
| 🐝 Assign workers | Queen calls `assign_task` to dispatch work to CLI agents |
| ⏳ Wait for results | Queen calls `wait_for_workers` to block until workers finish |
| 🔍 Review output | Queen calls `get_task_output`, then `approve_task` or `reject_task` |
| 🔄 Iterate | Queen creates more tasks or assigns retries as needed |
| ✅ Complete | Queen calls `complete` when the objective is satisfied |

The Queen also has `read_file` and `list_files` tools to inspect the project directly.

A **legacy mode** (`--legacy`) is available that uses a structured Plan → Delegate → Monitor → Review → Replan loop instead of autonomous tool use.

---

## Installation

### Prerequisites

- **Go 1.21+** — [Install Go](https://go.dev/doc/install)
- **An LLM API key** — Anthropic, OpenAI, or Gemini (for the Queen)
- **A worker CLI** (optional) — Claude Code, Kimi, Codex, etc. (or use `exec` for shell commands)

### Install from GitHub

```bash
# Install directly with go install
go install github.com/HexSleeves/waggle/cmd/waggle@latest

# Verify installation
waggle --version
```

### Build from Source

```bash
# Clone the repository
git clone https://github.com/HexSleeves/waggle.git
cd waggle

# Build the binary
go build -o waggle ./cmd/waggle/

# Optional: Install to your GOPATH/bin
go install ./cmd/waggle/

# Or move to a directory in your PATH
sudo mv waggle /usr/local/bin/
```

### Run Locally (Development)

```bash
# Clone and enter the repo
git clone https://github.com/HexSleeves/waggle.git
cd waggle

# Run directly without building
go run ./cmd/waggle/ --help

# Run tests
go test ./...

# Run with race detector
go test -race ./...

# Format and lint
gofmt -w .
go vet ./...
```

---

## Quick Start

### 1. Initialize a Hive

```bash
cd /path/to/your/project
waggle init
```

This creates a `waggle.json` configuration file in your project.

### 2. Set Your API Key

```bash
# Option 1: Environment variable (recommended)
export ANTHROPIC_API_KEY="sk-ant-..."

# Option 2: Edit waggle.json directly
# Set queen.api_key in the config file
```

### 3. Run Your First Objective

```bash
# Basic run — Queen plans and workers execute
waggle run "Add error handling to all database functions"

# Specify a worker adapter
waggle --adapter kimi run "Write unit tests for the auth module"

# Use shell commands directly (no AI CLI needed)
waggle --adapter exec run "Run the test suite and fix any failures"

# Increase parallelism
waggle --workers 8 run "Refactor all handlers to use the new logger"
```

### 4. Monitor Progress

Waggle displays a **TUI dashboard** showing:
- Queen's reasoning and tool calls
- Task progress and dependencies
- Worker status and output

Use `--plain` for CI environments or piped output:

```bash
waggle --plain run "Update documentation"
```

### Example Commands

```bash
# Run with pre-defined tasks (skips AI planning)
waggle --tasks tasks.json run "Execute CI pipeline"

# Force legacy orchestration mode
waggle --legacy run "Review code for security issues"

# Check session status
waggle status

# Resume a previous session
waggle resume abc123

# List recent sessions
waggle sessions

# View configuration
waggle config
```

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                       USER OBJECTIVE                            │
│              "Refactor the auth module to use JWT"              │
└──────────────────────────────┬──────────────────────────────────┘
                               │
                    ┌──────────▼──────────┐
                    │     👑 THE QUEEN     │
                    │   (tool-using LLM)  │
                    │                     │
                    │  create_tasks       │
                    │  assign_task        │
                    │  wait_for_workers   │
                    │  approve / reject   │
                    │  read_file          │
                    │  complete / fail    │
                    └───┬─────┬─────┬─────┘
                        │     │     │
              ┌─────────┘     │     └─────────┐
              ▼               ▼               ▼
     ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
     │ 🐝 Worker Bee │ │ 🐝 Worker Bee │ │ 🐝 Worker Bee │
     │  (kimi)      │ │   (codex)    │ │   (exec)     │
     └──────┬───────┘ └──────┬───────┘ └──────┬───────┘
            │                │                │
     ┌──────▼────────────────▼────────────────▼──────┐
     │              🍯 THE HIVE (.hive/)              │
     │           SQLite state · Blackboard            │
     └────────────────────────────────────────────────┘

     ┌────────────────────────────────────────────────┐
     │              🖥️  TUI Dashboard                  │
     │   👑 Queen panel  │  📋 Task panel  │  Status   │
     └────────────────────────────────────────────────┘
```

### 📚 Interactive Architecture Documentation

**[View the full architecture documentation →](ARCHITECTURE.md)**

For an interactive visualization with Mermaid diagrams, see:
**[architecture-diagram.html](architecture-diagram.html)** — System overview, execution flow, task state machine, and module breakdown.

### Internal Modules

| Module | Purpose |
|--------|---------|
| `queen` | 👑 Central orchestrator — autonomous LLM agent loop |
| `llm` | 🧠 Provider-agnostic LLM interface (Anthropic, OpenAI, Gemini) |
| `task` | 📋 Task graph with dependency DAG and status tracking |
| `worker` | 🐝 Worker pool for parallel CLI process execution |
| `adapter` | 🔌 CLI wrappers (Claude, Kimi, Codex, Gemini, Exec) |
| `bus` | 📨 In-process pub/sub event system |
| `blackboard` | 📝 Shared memory for inter-agent coordination |
| `state` | 💾 SQLite persistence (WAL mode) |
| `safety` | 🛡️ Path allowlist & command blocklist |
| `config` | ⚙️ Configuration management |
| `compact` | 📦 Context window compaction |
| `errors` | 🚨 Error classification & retry logic |
| `tui` | 🖥️ Bubble Tea terminal dashboard |
| `output` | 📤 Output mode management |

---

## Configuration

Running `waggle init` creates a `waggle.json` configuration file:

```json
{
  "project_dir": ".",
  "hive_dir": ".hive",
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
    "default_adapter": "claude-code"
  },
  "adapters": {
    "claude-code": { "command": "claude", "args": ["-p"] },
    "kimi":        { "command": "kimi",   "args": ["--print", "--final-message-only", "-p"] },
    "gemini":      { "command": "gemini" },
    "codex":       { "command": "codex",  "args": ["exec"] },
    "opencode":    { "command": "opencode", "args": ["run"] },
    "exec":        { "command": "bash" }
  },
  "safety": {
    "allowed_paths": ["."],
    "blocked_commands": ["rm -rf /", "sudo rm"],
    "read_only_mode": false,
    "max_file_size": 10485760
  }
}
```

### Queen LLM Providers

The Queen's own LLM is separate from worker adapters. Providers with **tool-use support** enable agent mode:

| Provider | Config Value | Tool Use | Environment Variable |
| -------- | ------------ | -------- | -------------------- |
| Anthropic API | `"anthropic"` | ✅ | `ANTHROPIC_API_KEY` |
| OpenAI API | `"openai"` | ✅ | `OPENAI_API_KEY` |
| Codex (OpenAI) | `"codex"` | ✅ | `OPENAI_API_KEY` |
| Gemini API | `"gemini-api"` | ✅ | `GEMINI_API_KEY` |
| Kimi CLI | `"kimi"` | ❌ | — |
| Claude CLI | `"claude-cli"` | ❌ | — |
| Gemini CLI | `"gemini"` | ❌ | — |

### Worker Adapters

| Adapter | CLI | Command | Notes |
| ------- | --- | ------- | ----- |
| `claude-code` | Claude Code | `claude -p "<prompt>"` | Default |
| `kimi` | Kimi Code | `kimi --print --final-message-only -p "<prompt>"` | Fast (~60s/task) |
| `gemini` | Gemini CLI | `echo "<prompt>" \| gemini` | Pipe-based |
| `codex` | Codex | `codex exec "<prompt>"` | |
| `opencode` | OpenCode | `opencode run "<prompt>"` | |
| `exec` | Shell | `bash -c "<description>"` | No AI — runs commands directly |

### Configuration Options

| Section | Key | Description |
| ------- | --- | ----------- |
| `queen.provider` | LLM provider | `anthropic`, `openai`, `gemini-api`, etc. |
| `queen.model` | Model name | e.g., `claude-sonnet-4-20250514` |
| `queen.api_key` | API key | Or use environment variable |
| `queen.max_iterations` | Loop limit | Hard cap on agent turns |
| `workers.max_parallel` | Pool size | Concurrent workers |
| `workers.default_adapter` | Default adapter | Which CLI to use |
| `workers.max_retries` | Retry limit | Per-task retry count |
| `safety.allowed_paths` | Path allowlist | Directories workers can touch |
| `safety.blocked_commands` | Command blocklist | Patterns to reject |

---

## Task File Format

Pre-define parallel tasks with dependencies:

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
    "description": "go test -race ./...",
    "priority": 3,
    "depends_on": ["lint"]
  },
  {
    "id": "build",
    "type": "code",
    "title": "Build binary",
    "description": "go build -o waggle ./cmd/waggle/",
    "priority": 1,
    "depends_on": ["test"]
  }
]
```

Run with:
```bash
waggle --tasks tasks.json run "Execute build pipeline"
```

---

## Queen's Tools

In agent mode, the Queen has 11 tools:

| Tool | Purpose |
| ---- | ------- |
| `create_tasks` | Create tasks with types, priorities, dependencies |
| `assign_task` | Dispatch a pending task to a worker |
| `wait_for_workers` | Block until workers complete |
| `get_status` | Get current status of all tasks |
| `get_task_output` | Read task output or error |
| `approve_task` | Mark a task as approved |
| `reject_task` | Reject with feedback, re-queue for retry |
| `read_file` | Read a project file (safety-checked) |
| `list_files` | List directory contents |
| `complete` | Declare objective complete |
| `fail` | Declare objective failed |

---

## Persistence

All state lives in `.hive/`:

```
.hive/
└── hive.db          # SQLite database (WAL mode)
```

The database stores:
- **Sessions** — objective, status, phase, iteration count
- **Tasks** — full state including results, retries, errors
- **Events** — append-only audit log
- **Messages** — conversation history for session resume

Resume interrupted sessions:
```bash
waggle resume <session-id>
```

---

## Safety

The **Safety Guard** enforces constraints on every worker operation:

- **Path allowlist** — workers can only touch files within configured directories
- **Command blocklist** — rejects commands matching dangerous patterns
- **File size limits** — prevents reading/writing files above threshold (default: 10 MB)
- **Read-only mode** — blocks all write operations when enabled

---

## Project Structure

```
waggle/
├── cmd/waggle/              # CLI entry point
│   ├── main.go
│   ├── app.go               # urfave/cli app + flags
│   ├── commands.go          # init, run, resume handlers
│   └── status.go            # session/task status display
├── internal/
│   ├── queen/               # 👑 Orchestration
│   ├── llm/                 # 🧠 LLM clients
│   ├── task/                # 📋 Task graph
│   ├── worker/              # 🐝 Worker pool
│   ├── adapter/             # 🔌 CLI adapters
│   ├── bus/                 # 📨 Event bus
│   ├── blackboard/          # 📝 Shared memory
│   ├── state/               # 💾 SQLite persistence
│   ├── safety/              # 🛡️ Security guard
│   ├── config/              # ⚙️ Configuration
│   ├── compact/             # 📦 Context compaction
│   ├── errors/              # 🚨 Error handling
│   ├── tui/                 # 🖥️ Terminal UI
│   └── output/              # 📤 Output management
├── ARCHITECTURE.md          # Detailed module documentation
├── architecture-diagram.html # Interactive diagrams
├── waggle.json              # Configuration file
└── go.mod
```

---

## Documentation

- **[ARCHITECTURE.md](ARCHITECTURE.md)** — Detailed breakdown of all internal modules
- **[architecture-diagram.html](architecture-diagram.html)** — Interactive Mermaid diagrams
- **[TODO.md](TODO.md)** — Development roadmap and task tracking

---

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Run tests (`go test ./...`)
4. Format code (`gofmt -w .`)
5. Commit changes (`git commit -m 'Add amazing feature'`)
6. Push to branch (`git push origin feature/amazing-feature`)
7. Open a Pull Request

---

## License

MIT
