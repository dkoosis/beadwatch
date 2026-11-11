.DEFAULT_GOAL := check

# Shared sandbox: cross / cross-amd64 / cross-arm64 and doctor.
include .sandbox/lib/Makefile.doctor.mk
include .sandbox/lib/Makefile.cross.mk

.PHONY: help check audit deploy build install lint race fuzz dupe nilcheck deadcode omitempty standard test vet vuln clean

SHELL := /bin/bash
.SHELLFLAGS := -euo pipefail -c

# Tool pins. The checker and the audit tools run at these pins through go run
# or npx, so no one installs them by hand. golangci-lint's pin is in
# .sandbox/project.conf.
VER := v0.4.0
OMITEMPTYSCALAR_VER := v0.4.0
NILAWAY_VER := v0.0.0-20260918162853-acb8859b9031
GOVULNCHECK_VER := v1.9.0
DEADCODE_VER := v0.52.0
JSCPD_VER := 5.1.2

help: ## Show this help
	@printf '\n\033[1mFour verbs. Identical in every dkoosis repo.\033[0m\n\n'
	@printf '  \033[36mcheck \033[0m  fast gate: vet + lint + test + build + repo-standard.\n'
	@printf '  \033[36maudit \033[0m  check, plus race + fuzz + dupe + nilcheck + deadcode + omitempty + vuln.\n'
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

# check is the full local gate: vet, lint, test, build, then repo-standard.
check: vet lint test build standard ## Fast gate: vet + lint + test + build + repo-standard
	@echo "=== check pass ==="

install: ## Install beadwatch to GOPATH/bin
	go install ./cmd/beadwatch

# Fleet gate: the checker runs at the pinned VER; bumping it is a deliberate PR.
standard: ## Run repo-standard (fleet checker) against this repo
	go run github.com/dkoosis/repo-standard/cmd/repo-standard@$(VER)

# vuln runs last in audit: govulncheck needs vuln.go.dev, so the other gates report first.
vuln: ## Scan for known vulnerabilities (govulncheck, pinned)
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VER) ./...

# Runs each Fuzz function it finds, FUZZTIME apiece.
FUZZTIME ?= 10s
fuzz: ## Fuzz every Fuzz test (FUZZTIME=10s each)
	@set -e; \
	for pkg in $$(go list ./...); do \
		list=$$(go test -list '^Fuzz' $$pkg) || exit $$?; \
		for f in $$(printf '%s\n' "$$list" | sed -n '/^Fuzz/p'); do \
			echo "fuzz $$pkg $$f"; \
			go test -run '^$$' -fuzz "^$$f$$" -fuzztime=$(FUZZTIME) $$pkg; \
		done; \
	done

dupe: ## Check for code duplication (jscpd, pinned)
	npx --yes jscpd@$(JSCPD_VER) . --silent --reporters console --ignore '**/.git/**,**/.sandbox/**,**/.beads/**,**/.golangci-cache/**,**/.golangci-rules/**,**/bin/**'

nilcheck: ## Run nilaway (pinned)
	go run go.uber.org/nilaway/cmd/nilaway@$(NILAWAY_VER) -include-pkgs=github.com/dkoosis/beadwatch ./...

# deadcode lists each function no path from main reaches, and exits 0 either way,
# so the target fails on any output.
deadcode: ## Fail on any function unreachable from main (deadcode, pinned)
	@out=$$(go run golang.org/x/tools/cmd/deadcode@$(DEADCODE_VER) ./cmd/beadwatch) || { echo "deadcode: could not run"; exit 1; }; \
	if [ -n "$$out" ]; then echo "$$out"; echo "deadcode: the functions above are unreachable; cut them"; exit 1; fi

# omitemptyscalar exits 3 on a ,omitempty tag on a bool (a false lost on write).
omitempty: ## Fail on a bool field tagged omitempty (omitemptyscalar, pinned)
	go run github.com/dkoosis/repo-standard/cmd/omitemptyscalar@$(OMITEMPTYSCALAR_VER) ./...

audit: check race fuzz dupe nilcheck deadcode omitempty vuln ## Exhaustive validation: check + race + fuzz + dupe + nilcheck + deadcode + omitempty + vuln
	@echo "=== audit pass ==="

deploy: build install ## Build, then install beadwatch to GOPATH/bin
	@echo "=== deployed ==="

clean: ## Remove built binary and sandbox build artifacts
	@rm -rf bin/beadwatch .sandbox/bin/linux-amd64 .sandbox/bin/linux-arm64 .sandbox/cache
	@echo "=== clean ==="
