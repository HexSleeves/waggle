# Waggle — TODO

> Prioritized next steps. Updated 2026-02-15.

## P0 — Critical (data loss / correctness)

- [x] **Fix session status: "stopped" vs "done"** — `Close()` now reads current status before overwriting. Terminal states preserved. 3 unit tests. *(done 2026-02-13)*

- [x] **Remove JSONL store** — Deleted `state.go` and all `store.Append()` calls. SQLite is the sole persistence layer. *(done 2026-02-15)*

- [x] **Wrap SQLite multi-statement ops in transactions** — `UpdateTaskStatus`, `UpdateTaskErrorType`, `IncrementTaskRetry` now use `BEGIN`/`COMMIT` with rollback. *(done 2026-02-15)*

- [x] **Add context.Context to all DB methods** — All public `DB` methods now accept `ctx context.Context`. Uses `ExecContext`/`QueryContext`/`BeginTx` throughout. All callers updated. *(done 2026-02-15)*

- [x] **Fix TaskRouter ignoring default_adapter** — `NewTaskRouter` was hardcoding `claude-code` for all task types. Now initializes routes from `workers.default_adapter` config. Agent mode `assign_task` tool now respects the configured adapter. *(done 2026-02-15)*

- [x] **Fix TUI auto-closing on completion** — Bubble Tea exited immediately when the Queen finished because tick commands stopped being issued. Now keeps ticking so the user can read results and press any key to exit. *(done 2026-02-15)*

## P1 — High (reliability / usability)

- [x] **Enforce safety guard in worker execution** — Wired into all 6 adapters. `ValidateTaskPaths()` and `CheckCommand()` called before spawn. *(done 2026-02-13)*

- [x] **Recover from handler panics in message bus** — `Publish()` wraps handlers with defer/recover. 5 tests. *(done 2026-02-13)*

- [x] **Detect circular task dependencies** — DFS-based `DetectCycles()` on TaskGraph, called from `parsePlanOutput()`. 12 tests. *(done 2026-02-14)*

- [x] **LLM-backed review phase** — Queen evaluates worker output for scope/correctness via `reviewWithLLM()`. Rejected tasks re-queued with feedback. *(done 2026-02-14)*

- [x] **LLM-backed replan phase** — After all tasks complete, Queen checks if more work needed via `replanWithLLM()`. *(done 2026-02-14)*

- [x] **Provider-agnostic LLM client** — `llm.Client` interface with Anthropic SDK and CLI adapter backends. Factory selects by `queen.provider` config. *(done 2026-02-14)*

- [x] **Agent mode (Queen as tool-using LLM)** — `RunAgent()` loop where the Queen autonomously calls tools. 11 tool definitions. Supports Anthropic, OpenAI, Gemini providers. Conversation compaction at 80+ messages. *(done 2026-02-15)*

- [x] **OpenAI and Gemini LLM providers** — `openai.go` and `gemini.go` with full tool-use support. Factory updated. *(done 2026-02-15)*

- [x] **TUI dashboard** — Bubble Tea-based terminal UI with Queen panel (thinking/tools/results), task panel (status/workers), status bar. Auto-detects TTY, `--plain` flag for CI. Message buffering for pre-start events. *(done 2026-02-15)*

- [x] **Fix pre-existing test failures** — Panics now classified as retryable (transient). Resume test fixed with correct session ID seeding. All tests green. *(done 2026-02-15)*

- [ ] **Implement real session resume** — CLI now loads from SQLite, but needs end-to-end testing. Verify interrupted sessions actually resume correctly.

- [x] **Add `--quiet` / `--json` output modes** — Monitoring logs are noisy for CI/scripting. Add quiet mode (completions only) and JSON mode.

## P2 — Medium (quality / testing)

- [ ] **Add unit tests for queen orchestrator** — Only Close() tests exist. Mock adapter registry and test Plan→Delegate→Monitor→Review loop with mock Bee.

- [ ] **Add unit tests for config, safety, compact** — All three have 0% test coverage. Pure logic, easy to test.

- [ ] **Add worker-level timeout with kill** — Monitor has global timeout but individual workers can run forever. Add per-task timeout enforcement.

- [ ] **Cap large worker output** — No limit on `result.Output` size. Truncate at configurable limit (e.g., 1MB).

- [ ] **Review rejection integration test** — Test that a rejected task actually gets re-queued with feedback and re-executed.

- [ ] **TUI resume mode** — Wire TUI into `cmdResume` (currently only plain mode gets TUI).

## P3 — Low (polish / extensibility)

- [ ] **Add Makefile** — `make build`, `make test`, `make install`, `make lint`.

- [ ] **Add CI with GitHub Actions** — Run `go vet`, `go test ./...`, `go build` on every push.

- [ ] **Publish binary releases** — GoReleaser or GH Actions workflow for linux/mac/arm64 binaries.

- [ ] **Add `waggle logs` command** — Stream or tail event log for a session from SQLite.

- [ ] **Add `waggle sessions` command** — List all past sessions with objective, status, task counts.

- [ ] **LLM-backed context summarizer** — Replace `compact.DefaultSummarizer` (extractive) with one that calls the Queen's LLM.

- [ ] **Support mixed adapters per task type** — Allow config like `"code": "kimi", "test": "exec"` so coding tasks use AI while test tasks just run `go test`.

- [ ] **Add progress bar / ETA** — Show `[3/5 tasks complete, ~2 min remaining]` instead of `⏳ 2 workers active`.

- [ ] **Adapter health check on startup** — Verify chosen adapter can run a trivial prompt before planning.

- [ ] **Add `--dry-run` flag** — Run planning only, show task graph, don't execute.

- [ ] **Reduce adapter boilerplate** — All 6 adapters are ~180 lines of nearly identical code. Extract `GenericCLIAdapter` base.

## Architectural Debt

- The Queen uses the worker adapter for planning in legacy mode (spawns a "planner" worker). Agent mode avoids this by using the Queen's own LLM client.
- The `compact.Context` is wired into the Queen but never read for decision-making. It's write-only.
- The blackboard is both in-memory and persisted (SQLite). On resume, in-memory starts empty.
- The Queen's `model` config field is only used when provider is `anthropic`, `openai`, or `gemini`. CLI-based providers ignore it.
- `cmdResume` doesn't use the TUI yet (only plain mode).
