# Queen Bee — TODO

> Prioritized next steps. Findings sourced from Kimi workers' own review of this codebase.

## P0 — Critical (data loss / correctness)

- [ ] **Add fsync to JSONL event append** — `state.go` writes events without `Sync()`. OS crash loses buffered data. Either add `logFile.Sync()` after each write, or remove the JSONL store entirely now that SQLite handles persistence.

- [ ] **Wrap SQLite multi-statement ops in transactions** — `db.go` methods like `UpdateTaskErrorType` do multiple UPDATEs without a transaction. Crash between them leaves inconsistent state. Add `tx.Begin()/tx.Commit()` wrappers.

- [ ] **Add context.Context to all DB methods** — Currently none of the `DB` methods accept a context, so queries can't be cancelled during shutdown. Change signatures to `GetSession(ctx, id)`, `AppendEvent(ctx, ...)`, etc.

- [ ] **Fix session status: "stopped" vs "done"** — `Close()` always sets status to "stopped" even after a successful run that already set "done". The `Close()` call overwrites it. Guard with `if currentStatus != "done"`.

## P1 — High (reliability / usability)

- [ ] **Enforce safety guard in worker execution** — `internal/safety` is fully implemented but never called. Wire `Guard.CheckPath()` and `Guard.CheckCommand()` into the adapter spawn path so workers are actually sandboxed.

- [ ] **Recover from handler panics in message bus** — `bus.go` calls handlers directly. A panicking handler crashes the entire bus and all subscribers. Add `defer recover()` in the handler dispatch loop.

- [ ] **Remove or deprecate JSONL store** — SQLite is now the source of truth. The JSONL store writes in parallel but nothing reads it (status command uses DB first). Remove it to reduce write amplification and complexity.

- [ ] **Implement real session resume** — `queen-bee resume` currently just re-runs with a placeholder objective. Load the actual task graph from SQLite, skip completed tasks, resume from where it left off.

- [ ] **Add `--quiet` / `--json` output modes** — The monitoring logs are noisy for CI/scripting. Add a quiet mode that only shows completions, and a JSON mode for machine consumption.

## P2 — Medium (quality / testing)

- [ ] **Add unit tests for queen package** — 0% coverage on the core orchestrator. Mock the adapter registry and test the Plan→Delegate→Monitor→Review loop with a mock Bee that returns canned results.

- [ ] **Add unit tests for config, safety, compact** — All three have 0% test coverage. These are pure logic modules that are easy to test.

- [ ] **Fix test files from opencode workers** — opencode created test files during a live review run that have compilation errors (`queen_test.go` references `bus.Len()` which doesn't exist). Clean up or fix these.

- [ ] **Detect circular task dependencies** — If the LLM generates a cycle in `depends_on`, the system deadlocks (no tasks ever become ready). Add cycle detection in `parsePlanOutput()`.

- [ ] **Add worker-level timeout with kill** — The monitor has a global timeout but individual workers can run forever within that window. Add per-task timeout enforcement that kills the underlying process.

- [ ] **Cap large worker output** — No limit on `result.Output` size. A worker producing 100MB of output will blow up memory. Truncate at a configurable limit (e.g., 1MB).

## P3 — Low (polish / extensibility)

- [ ] **Add Makefile** — `make build`, `make test`, `make install`, `make lint`.

- [ ] **Add CI with GitHub Actions** — Run `go vet`, `go test ./...`, `go build` on every push.

- [ ] **Publish binary releases** — Use GoReleaser or a simple GH Actions workflow to produce `queen-bee` binaries for linux/mac/arm64.

- [ ] **Add `queen-bee logs` command** — Stream or tail the event log for a session from SQLite. Useful for debugging.

- [ ] **Add `queen-bee sessions` command** — List all past sessions with their objective, status, task counts.

- [ ] **LLM-backed context summarizer** — Replace `compact.DefaultSummarizer` (extractive) with one that calls the configured adapter to produce actual summaries.

- [ ] **Support mixed adapters per task type** — Currently all tasks route to the same adapter. Allow config like `"code": "kimi", "test": "exec"` so coding tasks use AI while test tasks just run `go test`.

- [ ] **Add progress bar / ETA** — Show `[3/5 tasks complete, ~2 min remaining]` instead of just `⏳ 2 workers active`.

- [ ] **Adapter health check on startup** — Before planning, verify the chosen adapter can actually run a trivial prompt (catches auth failures early instead of failing mid-run).

- [ ] **Add `--dry-run` flag** — Run planning only, show the task graph, don't execute. Useful for previewing what the Queen will do.

- [ ] **Reduce adapter boilerplate** — All 7 adapters are ~140 lines of nearly identical code. Extract a `GenericCLIAdapter` base that handles spawn/monitor/kill/output, with adapters only defining command + args + path resolution.

## Architectural Debt

- The Queen uses the worker adapter for planning (spawns a "planner" worker). This means planning speed is gated by the adapter's latency. Consider a dedicated planning path that calls the LLM API directly.
- The `queen.Config.Model` and `queen.Config.Provider` fields are set but never used — the Queen has no direct LLM integration, it delegates everything through adapters.
- The `compact.Context` is wired into the Queen but the Queen never reads from it for decision-making. It's write-only currently.
- The blackboard is both in-memory (`internal/blackboard`) and persisted (SQLite `blackboard` table). The in-memory version is authoritative during a run, the DB is write-through. On resume, the in-memory blackboard starts empty.
