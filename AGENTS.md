# Agent Instructions for Waggle

## Before Committing

Always perform these steps before creating a commit:

1. **Format**: Run `gofmt -w .` to format all Go files.
2. **Lint**: Run `go vet ./...` and fix all reported issues.
3. **Test**: Run `go test ./...` and ensure all tests pass.
4. **Build**: Run `go build ./cmd/waggle/` to confirm compilation.
5. **Update TODO.md**: Mark completed items as done, add new items if work created them.
6. **Commit & Push**: Write a descriptive commit message, then `git push`.

## Code Style

- Follow standard Go conventions (gofmt, go vet).
- Tests go in `_test.go` files alongside the code they test.
- Use `t.Helper()` in test helpers.
- Use `t.TempDir()` or `os.MkdirTemp` for test directories.

## Project Structure

See README.md for full architecture. Key packages:
- `cmd/waggle/` — CLI entry point
- `internal/queen/` — Orchestration (agent.go, queen.go, tools.go)
- `internal/adapter/` — CLI adapters (generic.go is the base)
- `internal/state/` — SQLite persistence
- `internal/tui/` — Bubble Tea TUI
