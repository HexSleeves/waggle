# Waggle — TODO

> Prioritized next steps for the next agent. Updated 2026-02-16 (session 2).

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
- [x] Session resume E2E — 7 tests covering interrupted session continuity, task state restore, conversation history
- [x] TUI resume mode — `cmdResume` wired into TUI with `runResumeTUI`, shared `startQueenWithFunc` helper
- [x] Adapter health check — `HealthCheck()` on `CLIAdapter`, `setupAdapters()` extracted, fails fast before planning
- [x] `waggle sessions` — list past sessions with task counts, JSON output support
- [x] `waggle logs` — tail/stream event log with `--follow`, emoji icons, JSON output
- [x] Critical bug fixes (PR1) — 8 fixes: multi-word objectives, runJSON panic, runErr race, idempotent Close, assignment cleanup, max-iterations status, ListSessions NULL

## P1 — High (next up)

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

## VM Notes

- **Disk space**: ~19GB total, can fill up. Run `go clean -cache` to reclaim ~1GB.
- **Auth**: kimi is rate-limited, claude-code needs `/login`, no API keys set for Anthropic/OpenAI/Gemini.
- **exec adapter always works** for testing.
- **PAT**: Needs `workflow` scope to push `.github/workflows/` changes.
