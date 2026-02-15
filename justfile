# Waggle — multi-agent orchestration

default:
    @just --list

# Build the waggle binary
build:
    go build -o waggle ./cmd/waggle/

# Build with version info
build-release version="dev":
    go build -ldflags "-s -w -X main.Version={{version}}" -o waggle ./cmd/waggle/

# Run with an objective
run *ARGS:
    go run ./cmd/waggle/ {{ARGS}}

# Run interactively (TUI prompt)
run-interactive:
    go run ./cmd/waggle/

# Run with plain output (no TUI)
run-plain *ARGS:
    go run ./cmd/waggle/ --plain {{ARGS}}

# Run all tests
test:
    go test ./... -count=1 -timeout 60s

# Run tests with verbose output
test-verbose:
    go test ./... -count=1 -timeout 60s -v

# Run tests for a specific package
test-pkg pkg:
    go test ./internal/{{pkg}}/ -count=1 -timeout 30s -v

# Run tests with race detector
test-race:
    go test ./... -count=1 -timeout 120s -race

# Run go vet
vet:
    go vet ./...

# Format all Go files
fmt:
    gofmt -s -w .

# Check formatting (CI)
fmt-check:
    @test -z "$(gofmt -l .)" || (echo "Files need formatting:"; gofmt -l .; exit 1)

# Initialize a hive in the current directory
init:
    go run ./cmd/waggle/ init

# Show session status
status:
    go run ./cmd/waggle/ status

# Clean build artifacts
clean:
    rm -f waggle
    rm -rf .hive/

# Tidy go modules
tidy:
    go mod tidy

# Full CI check: fmt, vet, test
ci: fmt-check vet test
