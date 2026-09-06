.PHONY: build check install lint race test vet

# Per-worktree golangci-lint cache. Concurrent worktrees (dispatch/team runs)
# otherwise share one cache (~/.cache/golangci-lint); one worktree's cached
# analysis leaks stale file paths into another's run, so a clean worktree goes
# false-RED citing a sibling's files. Keying off $(CURDIR) gives each worktree
# its own cache, so contention can't happen. (Copied from strand's Makefile.)
GOLANGCI_LINT_CACHE := $(CURDIR)/.golangci-cache

build:
	go build -o bin/beadwatch ./cmd/beadwatch

test:
	go test ./...

# race runs the suite under the race detector. -count=1 bypasses the test
# cache so race runs on every invocation.
race:
	go test -race -count=1 ./...

# lint runs the strict golangci-lint set (.golangci.yml, copied from strand —
# bw-ajp — so both repos grade the same way).
# --allow-parallel-runners: golangci-lint's global single-instance lock exists
# to stop concurrent runs corrupting a shared cache. With the per-worktree
# cache above that risk is gone, so dispatch/team waves can lint concurrently.
lint:
	GOLANGCI_LINT_CACHE="$(GOLANGCI_LINT_CACHE)" golangci-lint run --allow-parallel-runners ./...

vet:
	go vet ./...

# check is the full local gate: vet, then lint, then test, then build.
check: vet lint test build
	@echo "=== check pass ==="

install:
	go install ./cmd/beadwatch
