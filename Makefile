.DEFAULT_GOAL := check

# Shared sandbox: cross / cross-amd64 / cross-arm64 and doctor.
include .sandbox/lib/Makefile.doctor.mk
include .sandbox/lib/Makefile.cross.mk

.PHONY: help check audit deploy build install lint pack-drift race selfcheck test vet vuln clean

help: ## Show this help
	@printf '\n\033[1mFour verbs. Identical in every dkoosis repo.\033[0m\n\n'
	@printf '  \033[36mcheck \033[0m  fast gate: vet + lint + pack-drift + test + build + conform.\n'
	@printf '  \033[36maudit \033[0m  check, plus race + vuln.\n'
	@printf '  \033[36mdeploy\033[0m  build + install beadwatch locally.\n'
	@printf '  \033[36mhelp  \033[0m  this text.\n\n'
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z0-9_.-]+:.*?## / { printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)
	@printf '\n'

# Per-worktree golangci-lint cache. Concurrent worktrees (dispatch/team runs)
# otherwise share one cache (~/.cache/golangci-lint); one worktree's cached
# analysis leaks stale file paths into another's run, so a clean worktree goes
# false-RED citing a sibling's files. Keying off $(CURDIR) gives each worktree
# its own cache, so contention can't happen. (Copied from strand's Makefile.)
GOLANGCI_LINT_CACHE := $(CURDIR)/.golangci-cache

build: ## Build bin/beadwatch
	go build -o bin/beadwatch ./cmd/beadwatch

test: ## Run tests
	go test ./...

# race runs the suite under the race detector. -count=1 bypasses the test
# cache so race runs on every invocation.
race: ## Run tests under the race detector (fresh run)
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

# check is the full local gate: vet, lint, pack-drift, test, build, then conform.
check: vet lint pack-drift test build selfcheck ## Fast gate: vet + lint + pack-drift + test + build + conform
	@echo "=== check pass ==="

install: ## Install beadwatch to GOPATH/bin
	go install ./cmd/beadwatch

pack-drift: ## Fail if the copied lintbrush pack rules drifted from upstream (network-soft)
	@.golangci-rules/check-pack-drift.sh

# Fleet gate: conform pinned as a go.mod tool dependency (go.sum-verified);
# bumping the pin is a deliberate PR.
selfcheck: ## Run conform (fleet SDLC checker) against this repo
	go tool conform-to-sdlc

vuln: ## Scan for known vulnerabilities
	govulncheck ./...

audit: check race vuln ## Exhaustive validation: check + race + vuln
	@echo "=== audit pass ==="

deploy: build install ## Build, then install beadwatch to GOPATH/bin
	@echo "=== deployed ==="

clean: ## Remove built binary and sandbox build artifacts
	@rm -rf bin/beadwatch .sandbox/bin/linux-amd64 .sandbox/bin/linux-arm64 .sandbox/cache
	@echo "=== clean ==="
