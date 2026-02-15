# Waggle — TODO

> Prioritized next steps for the next agent. Updated 2026-02-15.

## What's Done (don't redo these)

- [x] Core orchestration: Plan→Delegate→Monitor→Review loop + Agent mode (tool-using LLM)
- [x] 6 CLI adapters (claude, kimi, codex, opencode, gemini, exec) via shared `CLIAdapter`
- [x] TUI dashboard with Queen/worker panel switching, live streaming output, scroll
- [x] Interactive TUI mode (start without objective, prompt in TUI)
- [x] Per-worker timeout with kill (context.WithTimeout in Pool.Spawn)
- [x] Worker output capped at 1MB (configurable `workers.max_output_size`)
- [x] Parallel task execution (planning prompt + review→delegate shortcut)
- [x] Task retry with jittered exponential backoff (`RetryAfter` field)
- [x] Blackboard history capped at 10k entries
- [x] GitHub Actions CI (fmt-check + vet + test + build)
- [x] Justfile for build/test/run commands
- [x] Queen god-object split into delegate.go, planner.go, failure.go, reporter.go
- [x] Comprehensive test suite: 12,600 lines, 30 test files, all passing

## P1 — High (next up)

### 1. Session Resume End-to-End
The `waggle resume <session-id>` command exists but hasn't been validated end-to-end. Test:
- Start a run, interrupt it (Ctrl+C), resume it
- Verify task state is restored from SQLite
- Verify completed tasks aren't re-run
- Verify agent mode conversation history is restored from kv store
- Files: `cmd/waggle/commands.go` (cmdResume), `internal/queen/queen.go` (ResumeSession)

### 2. TUI Resume Mode
`cmdResume` currently only works with plain output. Wire TUI into resume path:
- Use same `runWithTUI` pattern from `commands.go`
- Files: `cmd/waggle/commands.go`

### 3. Adapter Health Check on Startup
Before planning, verify the chosen adapter can actually run. Currently if `claude` isn't logged in, the first task fails after a long planning phase.
- Add `HealthCheck()` to `CLIAdapter` that runs a trivial command (e.g., `claude -p "hi"`)
- Call it before `Run()`/`RunAgent()` starts
- Files: `internal/adapter/generic.go`, `internal/queen/queen.go`

## P2 — Medium

### 4. `waggle sessions` Command
List all past sessions with objective, status, task counts, timestamps.
- Query `sessions` table in SQLite
- Files: `cmd/waggle/` (new command), `internal/state/db.go` (new query)

### 5. `waggle logs` Command
Stream or tail event log for a session.
- Query `events` table filtered by session_id
- Files: `cmd/waggle/` (new command), `internal/state/db.go` (new query)

### 6. Review Rejection Integration Test
Test that a rejected task actually gets re-queued with feedback and re-executed by a new worker.
- Use exec adapter with a script that fails first time, succeeds second
- File: `internal/queen/orchestrator_test.go`

## P3 — Low

- [ ] **Binary releases** — GoReleaser or GH Actions for linux/mac/arm64
- [ ] **Mixed adapters per task type** — e.g., `"code": "kimi", "test": "exec"`
- [ ] **`--dry-run` flag** — Show planned task graph without executing
- [ ] **LLM-backed context summarizer** — Replace `compact.DefaultSummarizer`
- [ ] **Progress bar / ETA in TUI** — `[3/5 tasks, ~2 min remaining]`
- [ ] **Task dependency DAG visualization** — TUI or `dot` export

## Architectural Debt

- Legacy mode uses the worker adapter for planning (spawns a "planner" worker). Agent mode avoids this.
- `compact.Context` is write-only — wired into Queen but never read for decisions.
- Blackboard is in-memory + persisted. On resume, in-memory starts empty.
- `cmdResume` doesn't use TUI yet.

## VM Notes

- **Disk space**: ~19GB total, can fill up. Run `go clean -cache` to reclaim ~1GB.
- **Auth**: kimi is rate-limited, claude-code needs `/login`, no API keys set for Anthropic/OpenAI/Gemini.
- **exec adapter always works** for testing.
- **PAT**: Needs `workflow` scope to push `.github/workflows/` changes.
