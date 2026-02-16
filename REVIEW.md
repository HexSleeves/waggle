# Waggle — Code Review

> Principal-level review. 2026-02-16. ~23k lines Go, 30 test files.

---

## 1. Executive Summary

- **Task structs are shared-pointer mutable state with no synchronization.** `TaskGraph` returns `*Task` pointers, and the entire orchestrator (queen, tools, failure, delegate) mutates fields (`Result`, `RetryCount`, `Description`, `Status`) on these shared pointers without locks. This is the #1 systemic bug — it pervades every package.
- **`processWorkerResults()` never cleans up assignments** — stale entries cause duplicate processing.
- **`loadConversation()` for resume doesn't restore tool_result messages** — resumed conversations break LLM APIs that require tool_result after tool_use.
- **`compactMessages()` can split tool_use/tool_result pairs** — produces invalid conversations.
- **Multi-word objectives are silently truncated** — `args[0]` takes only the first word.
- **Data race on `runErr`** — goroutine writes, caller reads after `tuiProg.Run()`, no synchronization.
- **Panic in `runJSON`** — `q.Bus().History(1)[0]` index-out-of-range on empty history.
- **Double `q.Close()` in error paths** — `startQueenWithFunc` defers it, callers also call it.
- **`ListSessions` SUM returns NULL** for sessions with zero tasks — scans into `int`, driver-dependent.
- **Good foundations**: clean package separation, consistent adapter interface, SQLite WAL persistence, TUI architecture.

---

## 2. System Overview

```
cmd/waggle/       CLI + TUI wiring (7 files)
internal/queen/   Orchestration core — agent loop, legacy loop, tools, prompt, review (10 files)
internal/llm/     LLM clients — Anthropic, OpenAI, Gemini, CLI wrapper (7 files)
internal/adapter/ CLI adapter abstraction — generic + 6 adapters (8 files)
internal/worker/  Worker pool, bee interface (1 file)
internal/task/    Task graph with dependency tracking (1 file)
internal/state/   SQLite persistence (1 file)
internal/bus/     In-process pub/sub (1 file)
internal/tui/     Bubble Tea dashboard (5 files)
```

The Queen LLM calls tools → tools mutate task graph + spawn workers → workers execute via CLI adapters → results flow back through worker pool → Queen reviews/approves. All state persisted to SQLite for resume.

---

## 3. Findings Table

| # | Sev | Area | Finding | Impact | Recommendation | Effort |
|---|-----|------|---------|--------|----------------|--------|
| 1 | **CRIT** | concurrency | `*Task` shared pointers mutated without locks across queen, tools, failure, delegate | data races, corrupted state | Add mutex to Task, or copy-on-read from TaskGraph | L |
| 2 | **CRIT** | agent | `loadConversation()` omits tool_result messages | resume breaks LLM API (orphaned tool_use) | Persist full message pairs (user+assistant+tool_result) | M |
| 3 | **CRIT** | agent | `compactMessages()` can split tool_use/tool_result pairs | API errors mid-session | Compact in tool-call-aware chunks | M |
| 4 | **CRIT** | cmd | `args[0]` truncates multi-word objectives | `waggle run fix the auth bug` → objective is `"fix"` | `strings.Join(args, " ")` | S |
| 5 | **CRIT** | cmd | Panic: `History(1)[0]` on empty history in `runJSON` | crash in JSON mode | nil-check slice length | S |
| 6 | **CRIT** | cmd | Data race on `runErr` from `startQueenWithFunc` | undefined behavior | use channel or atomic | S |
| 7 | **HIGH** | tools | `processWorkerResults()` never deletes assignments | duplicate result processing, map grows | delete assignment after processing | S |
| 8 | **HIGH** | queen | `review()` LLM-rejection `continue` skips assignment cleanup | stale map entry, re-processing | move `delete` before `continue` | S |
| 9 | **HIGH** | queen | `q.phase`/`q.objective`/`q.iteration` written without lock, read in `Status()` under RLock | data race | hold write lock on mutation | S |
| 10 | **HIGH** | cmd | Double `q.Close()` — goroutine defers it, error paths also call it | panic or corrupt DB | make Close idempotent with sync.Once | S |
| 11 | **HIGH** | agent | `RunAgentResume()` has no retry on LLM failure (unlike `RunAgent`) | single transient error kills resume | add same retry logic | S |
| 12 | **HIGH** | queen | Session status not updated on max-iterations exit | session stuck in non-terminal state in DB | set status to "failed" | S |
| 13 | **HIGH** | worker | `Pool.Spawn()` TOCTOU on capacity check — unlock between check and insert | can exceed maxParallel | hold lock across check+insert | S |
| 14 | **HIGH** | worker | Context cancel leaked when `bee.Spawn()` fails in `Pool.Spawn()` | timer goroutine leak | call cancel on error path | S |
| 15 | **MED** | queen | `review()` nil-pointer: `t.Type` in `board.Post()` without nil check | panic if task missing | guard with nil check | S |
| 16 | **MED** | queen | `New()` leaks DB handle if `safety.NewGuard()` fails | fd leak | defer close on error | S |
| 17 | **MED** | state | `ListSessions` SUM returns NULL for zero-task sessions | scan fails or gets 0 depending on driver | wrap in `COALESCE(..., 0)` | S |
| 18 | **MED** | security | `gemini.go` puts API key in URL query string | key in logs/proxies | use header | S |
| 19 | **MED** | security | `safety.CheckPath` doesn't resolve symlinks | symlink can escape allowed dirs | call `filepath.EvalSymlinks()` | S |
| 20 | **MED** | llm | OpenAI and Gemini clients use `http.Client{}` with no timeout | hangs forever on unresponsive server | set 120s timeout | S |
| 21 | **MED** | tools | `handleWaitForWorkers` silently ignores JSON unmarshal error | bad input → 300s default timeout, no error | return parse error | S |
| 22 | **MED** | planner | `parsePlanOutput()` silently falls back to single task on LLM garbage | masks planning failure | log warning, set error flag | S |
| 23 | **MED** | planner | Task ID from `time.Now().UnixNano()` can collide | duplicate task IDs | use UUID or atomic counter | S |
| 24 | **MED** | cmd | `loadConfigFromCtx` calls `log.Fatalf` → `os.Exit(1)` | bypasses cleanup | return error | S |
| 25 | **MED** | tui | `wrapText` counts bytes not display width | emoji/CJK overflow terminal | use `runewidth` | M |
| 26 | **LOW** | tui | Dead types: `ObjectiveSubmittedMsg`, `WorkerUpdateMsg.Elapsed/.Adapter` | confusion | delete | S |
| 27 | **LOW** | queen | `ReviewVerdict.NewTasks` parsed but never used | wasted effort, missed feature | wire up or remove | S |
| 28 | **LOW** | queen | Direct `fmt.Printf` in `review()` bypasses logger, corrupts TUI | garbled output | use `q.logger` | S |
| 29 | **LOW** | cmd | Signal handler goroutine+registration leak (3 places) | benign for CLI, bad for tests | defer `signal.Stop` | S |
| 30 | **LOW** | cmd | Queen init boilerplate copy-pasted 6 times | maintenance burden | extract helper | M |
| 31 | **LOW** | state | `DB.Raw()` exposes internal `*sql.DB` | breaks encapsulation | remove or restrict | S |
| 32 | **LOW** | bus | No way to unsubscribe — handlers leak | irrelevant for CLI, bad for library use | add unsubscribe | M |
| 33 | **LOW** | compact | `compact.Context` not thread-safe | race if used concurrently | add mutex | S |
| 34 | **LOW** | task | `Task.Constraints`/`Context` not persisted by `InsertTask` | lost on resume | add columns or serialize | M |

---

## 4. Urgent: Must Fix Now

1. **Multi-word objectives** — `cmd/waggle/commands.go:101` and `app.go:145`: change `args[0]` to `strings.Join(args, " ")`.

2. **Panic in runJSON** — `commands.go:487`: guard `History(1)` with length check.

3. **Data race on runErr** — `commands.go` `startQueenWithFunc`: use a channel `errCh chan error` instead of returning `*error`.

4. **Double Close** — make `Queen.Close()` idempotent with `sync.Once`.

5. **processWorkerResults assignment cleanup** — `tools.go:615`: add `delete(q.assignments, workerID)` after processing each completed/failed worker.

6. **review() LLM rejection skips assignment cleanup** — `queen.go`: move `delete(q.assignments, workerID)` before the `continue`.

7. **Max-iterations session status** — `queen.go` end of `Run()`: add `q.db.UpdateSessionStatus(ctx, q.sessionID, "failed")`.

8. **ListSessions NULL** — wrap SUM columns in `COALESCE(SUM(...), 0)`.

---

## 5. Improvements: Optimize / Harden

1. **Task struct synchronization**: Either add a `sync.RWMutex` to `Task` and use getters/setters, or have `TaskGraph` return copies instead of pointers. The latter is simpler but means callers must `UpdateField()` through the graph.

2. **loadConversation**: Persist the full `[]llm.ToolMessage` conversation as a JSON blob in KV, not just assistant responses turn-by-turn. On resume, deserialize the whole conversation.

3. **compactMessages**: Find tool_use/tool_result boundaries and never split them. Walk backwards from the cut point to the nearest "clean" boundary (a user or standalone assistant message).

4. **Pool.Spawn TOCTOU**: Hold the lock across the capacity check, worker creation, and map insertion.

5. **HTTP client timeouts**: `openai.go`, `gemini.go`: set `&http.Client{Timeout: 120 * time.Second}`.

6. **Gemini API key in header**: Move from URL query param to `x-goog-api-key` header.

7. **Symlink resolution in CheckPath**: Call `filepath.EvalSymlinks()` after `filepath.Abs()`.

8. **RunAgentResume retry**: Add the same retry-once logic from `RunAgent`.

---

## 6. Cleanup: Condense / Refactor

1. **Queen init boilerplate** — Extract `newQueenForRun(cfg, tuiProg) (*Queen, error)` that handles: create Queen, set logger, suppress report, subscribe bus events.

2. **Signal handling** — Extract `withSignalCancel(ctx) (context.Context, func())` used by all run variants.

3. **`fmt.Printf` in review()** — Replace with `q.logger.Printf`.

4. **statusIcon duplication** — Move to a shared `internal/ui` package or delete one.

5. **Dead TUI types** — Delete `ObjectiveSubmittedMsg`, `WorkerUpdateMsg.Elapsed`, `WorkerUpdateMsg.Adapter`, `WorkerInfo.Adapter`.

---

## 7. Removals: Delete / Deprecate

- `state.DB.Raw()` — no callers outside tests, breaks encapsulation
- `ReviewVerdict.NewTasks` — parsed but discarded, remove until implemented
- `Program.SetQuiet()` — never called
- `adapter.CLIAdapterConfig.FallbackPaths` — never set by any adapter
- `compact.Context` — write-only, never read for decisions; either wire it up or remove

---

## 8. Refactoring Plan (PR-sized steps)

| PR | Scope | Risk | Changes |
|----|-------|------|---------|
| **PR1: Critical fixes** | Urgent items 1-8 from §4 | Low — all localized | commands.go, tools.go, queen.go, db.go |
| **PR2: Task synchronization** | Add mutex to Task or copy-on-read | Medium — touches many callers | task/task.go + all queen/*.go |
| **PR3: Conversation persistence** | Fix loadConversation + compactMessages | Medium — changes resume format | agent.go, db.go |
| **PR4: Worker pool fixes** | TOCTOU, context leak, Close idempotency | Low | worker.go, queen.go |
| **PR5: Security hardening** | Symlinks, Gemini API key, HTTP timeouts | Low | safety.go, gemini.go, openai.go |
| **PR6: Cleanup & dead code** | Boilerplate extraction, dead types, removals | Low | cmd/, tui/, queen/ |

Sequencing: PR1 → PR2 → PR3 → PR4/PR5 (parallel) → PR6.

---

## 9. Testing Plan

| Test | Type | Covers |
|------|------|--------|
| `TestMultiWordObjective` | unit | args joining in cmdRun/default action |
| `TestRunJSONEmptyHistory` | unit | History(1) guard |
| `TestStartQueenErrChannel` | unit | race-free error passing |
| `TestQueenCloseIdempotent` | unit | double Close doesn't panic |
| `TestProcessWorkerResultsCleansAssignments` | unit | assignment map cleanup |
| `TestCompactMessagesPreservesToolPairs` | unit | no orphaned tool_use/tool_result |
| `TestLoadConversationRestoresToolResults` | unit | full conversation roundtrip |
| `TestPoolSpawnRespectsConcurrencyLimit` | integration | maxParallel enforced under contention |
| `TestResumeE2EWithExecAdapter` | e2e | start → interrupt → resume → complete |
| `TestSymlinkEscapeBlocked` | unit | symlink outside allowed dir rejected |
| `go test -race ./...` | CI | add to GitHub Actions workflow |

---

## 10. Open Questions

1. **Is `compact.Context` intended to be used?** It's wired in but never read. If it's planned for future context window management, it needs a mutex. If not, it should be removed.

2. **Should blackboard state be restored on resume?** Currently in-memory blackboard starts empty after resume. If workers need inter-task communication across restarts, the SQLite blackboard table should be loaded.

3. **Is `DB.Raw()` used anywhere outside tests?** If not, it should be removed to maintain the abstraction boundary.

4. **What's the minimum Go version?** Affects `math/rand` seeding behavior (auto-seed in 1.20+) and available stdlib features.

5. **Are there plans for concurrent task creation in agent mode?** Currently the agent loop is single-threaded, but if parallel tool calls are ever supported, the Task mutation races become immediately exploitable.
