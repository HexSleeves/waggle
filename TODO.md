# Waggle — TODO

> Prioritized next steps. Updated 2026-02-16.
>
> Improvements inspired by [Shelley](https://github.com/boldsoftware/shelley) agent architecture.

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
- [x] Comprehensive test suite: 13,100 lines, 30 test files, all passing
- [x] Session resume E2E — 7 tests covering interrupted session continuity, task state restore, conversation history
- [x] TUI resume mode — `cmdResume` wired into TUI with `runResumeTUI`, shared `startQueenWithFunc` helper
- [x] Adapter health check — `HealthCheck()` on `CLIAdapter`, `setupAdapters()` extracted, fails fast before planning
- [x] `waggle sessions` — list past sessions with task counts, JSON output support
- [x] `waggle logs` — tail/stream event log with `--follow`, emoji icons, JSON output
- [x] Critical bug fixes (PR1) — 8 fixes: multi-word objectives, runJSON panic, runErr race, idempotent Close, assignment cleanup, max-iterations status, ListSessions NULL
- [x] Task synchronization (PR2) — mutex on Task struct, 14 thread-safe getters/setters, all callers updated
- [x] Conversation persistence (PR3) — persist full []ToolMessage, tool-aware compaction, legacy fallback
- [x] Worker pool fixes (PR4) — TOCTOU on capacity check, context cancel leak on spawn failure
- [x] REVIEW.md comprehensive fixes (PR5+PR6) — 20 issues resolved across security, concurrency, cleanup
- [x] Final REVIEW.md items (PR7) — remove DB.Raw(), bus unsubscribe, persist task constraints/context, extract Queen init helper

---

## P0 — Reliability (Agent Loop Hardening) ✅ DONE

- [x] **History repair** — `repairToolHistory()` fixes orphaned tool_use/tool_result pairs before every LLM call
- [x] **Max-tokens truncation** — Detects truncated responses, injects retry message, excludes from history
- [x] **Retry with error classification** — `internal/llm/retry.go` with `IsRetryableError()` + `RetryLLMCall()` (exponential backoff, 3 retries)

---

## P1 — Cost & Performance ✅ DONE

- [x] **Prompt caching** — `Cache bool` on ToolDef/ContentBlock, Anthropic sets CacheControlEphemeral, `applyCacheHints()` in agent loop
- [x] **Usage tracking** — `Usage` struct in Response, extracted from Anthropic/OpenAI/Gemini, accumulated per-session
- [x] **Tool timing** — Duration logged per tool call, aggregated summary at session end

---

## P2 — Robustness ✅ DONE

- [x] **Compaction fixes** — Forward-scan fallback for safe cut points, `LLMSummarizer` with fallback to `DefaultSummarizer`
- [x] **Large output handling** — `truncateLargeOutput()` saves to `.hive/outputs/`, returns head+tail to LLM
- [x] **Git state tracking** — `gitstate.go` with `GetGitState()`, `Diff()`, integrated into `wait_for_workers`

---

## P3 — UX & Observability ✅ DONE

- [x] **Dual-content model** — `ToolOutput{LLMContent, Display}` for all 11 handlers, TUI gets formatted display
- [x] **Per-message DB storage** — `messages` table with `AppendMessage`/`LoadMessages`/`MarkMessageExcluded`
- [x] **Read/write DB pool** — Separate writer (1 conn) and reader (3 conns) with WAL mode

---

## P3 — Low Priority

- [x] **Binary releases** — GoReleaser + GH Actions for linux/mac/arm64/windows
- [x] **Mixed adapters per task type** — `adapter_map` routes task types to specific adapters
- [x] **`--dry-run` flag** — Shows planned task graph without executing workers
- [ ] **Progress bar / ETA in TUI** — `[3/5 tasks, ~2 min remaining]`
- [ ] **Task dependency DAG visualization** — TUI or `dot` export
- [ ] **Review Rejection Integration Test** — test reject→re-queue→re-execute cycle

## Architectural Debt

- Legacy mode uses the worker adapter for planning (spawns a "planner" worker). Agent mode avoids this.
- Blackboard is in-memory + persisted. On resume, in-memory starts empty.

## VM Notes

- **Disk space**: ~19GB total, can fill up. Run `go clean -cache` to reclaim ~1GB.
- **Auth**: kimi is rate-limited, claude-code needs `/login`, no API keys set for Anthropic/OpenAI/Gemini.
- **exec adapter always works** for testing.
- **PAT**: Needs `workflow` scope to push `.github/workflows/` changes.
