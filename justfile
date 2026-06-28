# Default recipe: list all available commands
default:
    @just --list

# Build the rally binary
build:
    go build -o bin/rally ./cmd/rally

# Run rally with optional arguments (e.g. `just run init`)
run *args:
    go run ./cmd/rally {{args}}

# Run all tests
test:
    go test -count=1 ./...

# Reproduce the CI test job, including real-agent tests when local CLIs/auth exist
test-real:
    RALLY_TEST_REAL_AGENTS=1 go test -count=1 ./...

# Run tests in verbose mode
test-verbose:
    go test -v -count=1 ./...

# Format all Go source files
fmt:
    go fmt ./...

# Run static analysis (go vet)
vet:
    go vet ./...

# Check formatting and static analysis
check: vet
    @echo "==> Checking formatting..."
    @unformatted=$(gofmt -l .); \
    if [ -n "$unformatted" ]; then \
        echo "❌ The following files are not formatted correctly. Run 'just fmt':" >&2; \
        echo "$unformatted" >&2; \
        exit 1; \
    fi
    @echo "✅ All checks passed!"

# Run tests with the race detector enabled
test-race:
    go test -race -shuffle=on -count=1 ./...

# Run vulnerability scanner
# First run: go install golang.org/x/vuln/cmd/govulncheck@latest
audit:
    govulncheck ./...

# Verify go.mod and go.sum are tidy (no uncommitted changes after tidy)
tidy-check:
    go mod tidy
    git diff --exit-code go.mod go.sum

# Set up local Git hooks
setup-hooks:
    ./scripts/setup-hooks.sh

# Remove build artifacts
clean:
    rm -rf bin/
