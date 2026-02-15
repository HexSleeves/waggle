# Waggle — TODO

> Prioritized next steps. Updated 2026-02-15.

## Recently Completed

- [x] **Fix tasks running sequentially** — Planning prompt now tells LLM to maximize parallelism. Legacy mode skips Plan phase when ready tasks exist after review. Agent prompt emphasizes assigning all ready tasks before waiting. *(done 2026-02-15)*
- [x] **Stream worker output live** — All 6 adapters now use `streamWriter` + `io.MultiWriter` to tee stdout/stderr to `w.output` in real-time. TUI worker panels show live content. *(done 2026-02-15)*
- [x] **Fix TUI scroll eating messages** — Scroll offset clamped to `len(lines) - visibleHeight` so panel always stays full. *(done 2026-02-15)*
- [x] **Add justfile** — `just build`, `just test`, `just run`, `just ci`, etc. *(done 2026-02-15)*
- [x] **Add blackboard + worker pool tests** — 470 + 602 lines covering core functionality and concurrency. *(done 2026-02-15)*
- [x] **Fix flaky TestBuildPrompt** — Map iteration order assumption removed. *(done 2026-02-15)*

## P0 — Critical (data loss / correctness)

*Nothing critical outstanding.*

## P1 — High (reliability / usability)

- [ ] **Reduce adapter boilerplate** — All 6 adapters are ~170 lines of nearly identical code (spawn goroutine, stream output, classify errors). Extract `GenericCLIWorker` base that takes command + args, reducing each adapter to ~20 lines.

- [ ] **Implement real session resume** — CLI loads from SQLite but needs end-to-end testing. Verify interrupted sessions actually resume correctly with task state + conversation history.

- [ ] **Add worker-level timeout with kill** — Monitor has global timeout but individual workers can run forever. Add per-task timeout enforcement in the pool.

- [ ] **Cap large worker output** — No limit on `result.Output` size. Truncate at configurable limit (e.g., 1MB) to prevent memory issues with chatty workers.

- [ ] **Add CI with GitHub Actions** — Run `just ci` (fmt-check + vet + test) on every push. Low effort, high value.

## P2 — Medium (quality / testing)

- [ ] **Add unit tests for queen orchestrator** — Only Close() tests exist. Mock adapter registry and test Plan→Delegate→Monitor→Review loop with mock Bee.

- [ ] **Add unit tests for config, safety, compact** — All three have 0% test coverage. Pure logic, easy to test.

- [ ] **Review rejection integration test** — Test that a rejected task actually gets re-queued with feedback and re-executed.

- [ ] **TUI resume mode** — Wire TUI into `cmdResume` (currently only plain mode gets TUI).

- [ ] **Adapter health check on startup** — Verify chosen adapter can run a trivial prompt before planning. Fail fast instead of failing on first task.

## P3 — Low (polish / extensibility)

- [ ] **Publish binary releases** — GoReleaser or GH Actions workflow for linux/mac/arm64 binaries.

- [ ] **Add `waggle logs` command** — Stream or tail event log for a session from SQLite.

- [ ] **Add `waggle sessions` command** — List all past sessions with objective, status, task counts.

- [ ] **Support mixed adapters per task type** — Allow config like `"code": "kimi", "test": "exec"` so coding tasks use AI while test tasks just run `go test`.

- [ ] **Add progress bar / ETA** — Show `[3/5 tasks complete, ~2 min remaining]` instead of `⏳ 2 workers active`.

- [ ] **Add `--dry-run` flag** — Run planning only, show task graph, don't execute.

- [ ] **LLM-backed context summarizer** — Replace `compact.DefaultSummarizer` (extractive) with one that calls the Queen's LLM.

- [ ] **Configurable TUI theme** — Allow color scheme customization via config.

- [ ] **Task dependency visualization** — Show DAG structure in TUI or as `dot` graph export.

## Architectural Debt

- The Queen uses the worker adapter for planning in legacy mode (spawns a "planner" worker). Agent mode avoids this by using the Queen's own LLM client.
- The `compact.Context` is wired into the Queen but never read for decision-making. It's write-only.
- The blackboard is both in-memory and persisted (SQLite). On resume, in-memory starts empty.
- The Queen's `model` config field is only used when provider is `anthropic`, `openai`, or `gemini`. CLI-based providers ignore it.
- `cmdResume` doesn't use the TUI yet (only plain mode).
