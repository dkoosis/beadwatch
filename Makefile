.DEFAULT_GOAL := check

.PHONY: build check install lint race test vet selfcheck audit help deploy

# Per-worktree golangci-lint cache. Concurrent worktrees (dispatch/team runs)
# otherwise share one cache (~/.cache/golangci-lint); one worktree's cached
# analysis leaks stale file paths into another's run, so a clean worktree goes
# false-RED citing a sibling's files. Keying off $(CURDIR) gives each worktree
# its own cache, so contention can't happen. (Copied from strand's Makefile.)
GOLANGCI_LINT_CACHE := $(CURDIR)/.golangci-cache

help: ## Show this help
	@printf '\n\033[1mFour verbs. Identical in every dkoosis repo.\033[0m\n\n'
	@printf '  \033[36mcheck \033[0m  fast gate — vet + lint + test + build + conform. Pre-commit; required in CI.\n'
	@printf '  \033[36maudit \033[0m  check, plus race. Before you ask for review.\n'
	@printf '  \033[36mdeploy\033[0m  build + install this tool locally.\n'
	@printf '  \033[36mhelp  \033[0m  this text.\n\n'
	@printf 'Everything below is an internal step of one of those four. Call the verbs.\n\n'
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z0-9_.-]+:.*?## / { printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
	@printf '\n'

build: ## Compile everything
	go build -o bin/beadwatch ./cmd/beadwatch

test: ## Run tests
	go test ./...

# race runs the suite under the race detector. -count=1 bypasses the test
# cache so race runs on every invocation.
race: ## Run tests with race detector (fresh run)
	go test -race -count=1 ./...

# lint runs the strict golangci-lint set (.golangci.yml, copied from strand —
# bw-ajp — so both repos grade the same way).
# --allow-parallel-runners: golangci-lint's global single-instance lock exists
# to stop concurrent runs corrupting a shared cache. With the per-worktree
# cache above that risk is gone, so dispatch/team waves can lint concurrently.
lint: ## Run golangci-lint (strict config)
	GOLANGCI_LINT_CACHE="$(GOLANGCI_LINT_CACHE)" golangci-lint run --allow-parallel-runners ./...

vet: ## Run go vet
	go vet ./...

# check is the full local gate: vet, then lint, then test, then build, then conform.
check: vet lint test build selfcheck ## Fast validation: vet + lint + test + build + conform
	@echo "=== check pass ==="

# Fleet gate: conform pinned as a go.mod tool dependency (go.sum-verified);
# bumping the pin is a deliberate PR.
selfcheck: ## Run conform (fleet SDLC checker) against this repo
	go tool conform

audit: check race ## Exhaustive validation: check + race
	@echo "=== audit pass ==="

install: ## Install beadwatch to GOPATH/bin
	go install ./cmd/beadwatch

deploy: build install ## Build, then install beadwatch locally
	@echo "=== deployed ==="
