# Canonical non-secret CI budget, overridden locally by a private .env.
-include .env.ci
-include .env
export FLOWSTATE_PR_MAX_LINES

.PHONY: all build run test test-e2e fmt lint check clean help ai-commit check-ai-attribution list-ai-commits install-tools check-pr-size check-size-live install-hooks check-python-contract
.PHONY: check-admission-commits check-admission merge-pr preflight preflight-guard
.PHONY: package-admission-runner install-admission-runner admission-runner-start admission-runner-stop admission-runner-status

package-admission-runner: ## Package committed admission runner and its pinned dependency manifest
	python3 -B -m scripts.admission_service package

install-admission-runner: package-admission-runner ## Validate private config and install the packaged user service
	python3 -B -m scripts.admission_service install

admission-runner-start admission-runner-stop admission-runner-status: ## Manage the configured admission runner
	python3 -B -m scripts.admission_service $(patsubst admission-runner-%,%,$@)

check-admission-commits: ## Validate actual commit evidence before push (external cache or Dropbox read token)
	python3 -B -m scripts.admission_preflight --base "$(BASE_SHA)" --head "$(or $(HEAD_SHA),$(shell git rev-parse HEAD))"

check-admission: ## Validate live exact-head PR review/evidence/Build; never publish a status
	python3 -B -m scripts.admission_preflight --phase ready --repo baphled/FlowState --pr "$(PR)" --base "$(BASE_SHA)" --head "$(or $(HEAD_SHA),$(shell git rev-parse HEAD))"

merge-pr: ## Squash-merge an admitted PR carrying exactly one Project-Metadata trailer (PR=N [BASE=next] [DRY_RUN=1])
	bash scripts/merge_pr.sh --pr "$(PR)" --base "$(BASE)" $(if $(DRY_RUN),--dry-run)

check-pr-size: ## Bound the complete committed PR diff (BASE_SHA=actual target)
	python3 -B scripts/pr_size.py --base "$(BASE_SHA)" --head "$$(git rev-parse HEAD)"

check-size-live: ## Live working-tree (staged+unstaged+untracked) budget check, no commit needed
	@BASE="$${BASE_SHA:-$$(git merge-base HEAD origin/next 2>/dev/null || git rev-parse origin/next)}"; \
	python3 -B scripts/pr_size.py --live --base "$$BASE"

install-hooks: ## Point git at the repo-managed .githooks directory
	git config core.hooksPath .githooks

# Binary name
BINARY_NAME=flowstate
BUILD_DIR=./build

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOFMT=$(GOCMD) fmt
GOVET=$(GOCMD) vet
GOMOD=$(GOCMD) mod
GINKGO_PROCS ?= 2
GINKGO_COMPILERS ?= 2

# Default target
all: check build

#
# Build
#

build: ## Build the binary
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/flowstate

run: build ## Build and run the application
	@echo "Running $(BINARY_NAME)..."
	$(BUILD_DIR)/$(BINARY_NAME)

clean: ## Clean build artifacts
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -f coverage.out

#
# Testing
#

test: ## Run legacy Go tests and parallel Ginkgo suites during migration
	$(GOTEST) -race -count=1 ./...
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo -r --procs=$(GINKGO_PROCS) --compilers=$(GINKGO_COMPILERS) --randomize-all --randomize-suites --fail-on-pending --fail-on-empty --keep-going --race --timeout=5m

test-e2e: ## Run the CLI end-to-end matrix suite against the real built binary
	$(GOCMD) run github.com/onsi/ginkgo/v2/ginkgo --procs=$(GINKGO_PROCS) --compilers=$(GINKGO_COMPILERS) --label-filter=e2e --fail-on-pending --race --timeout=5m ./cmd/flowstate

check-test-contract: ## Check changed tests (BASE_SHA=exact PR base)
	python3 -B -m unittest discover -s scripts/tests -p test_test_contract.py
	python3 -B -m unittest discover -s scripts/tests -p test_test_layout.py
	python3 -B scripts/check_test_contract.py --base "$(BASE_SHA)"

test-coverage: ## Run tests with coverage
	@echo "Running tests with coverage..."
	$(GOTEST) -v -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out

#
# Code Quality
#

fmt: ## Format code
	@echo "Formatting code..."
	$(GOFMT) ./...

lint: ## Run linters
	@echo "Running linters..."
	$(GOVET) ./...
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./...; fi
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run; fi
	@if command -v deadcode >/dev/null 2>&1 && go list -f '{{if eq .Name "main"}}{{.ImportPath}}{{end}}' ./... 2>/dev/null | grep -q .; then deadcode ./...; fi

check: fmt lint test test-e2e ## Run all checks

# Local mirror of most blocking CI jobs, run on the committed HEAD. The tools
# are pinned to the versions CI uses and everything runs on the Go toolchain
# go.mod names: the pinned linter cannot read export data from a newer Go,
# which is how a machine with a newer system Go ends up with a linter that
# reports nothing but typecheck errors.
GOLANGCI_LINT_VERSION := v2.11.3
GOSEC_VERSION := v2.30.0
REPO_GOTOOLCHAIN := go$(shell sed -n 's/^go //p' go.mod)
TOOLS_BIN := $(CURDIR)/bin/tools
GOLANGCI_LINT := $(TOOLS_BIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)
GOSEC := $(TOOLS_BIN)/gosec-$(GOSEC_VERSION)

$(GOLANGCI_LINT):
	GOBIN=$(TOOLS_BIN) GOTOOLCHAIN=$(REPO_GOTOOLCHAIN) $(GOCMD) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	mv $(TOOLS_BIN)/golangci-lint $@

$(GOSEC):
	GOBIN=$(TOOLS_BIN) GOTOOLCHAIN=$(REPO_GOTOOLCHAIN) $(GOCMD) install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)
	mv $(TOOLS_BIN)/gosec $@

# CI sees the committed tree, so uncommitted edits must not decide the result.
preflight-guard:
	@test -n "$(BASE_SHA)" || { echo 'preflight: BASE_SHA=<PR base sha> is required' >&2; exit 2; }
	@test -z "$$(git status --porcelain)" || { echo 'preflight: it verifies the committed HEAD; commit or discard local changes first' >&2; exit 2; }

preflight: export GOTOOLCHAIN := $(REPO_GOTOOLCHAIN)
preflight: preflight-guard $(GOLANGCI_LINT) $(GOSEC) ## Run most blocking CI checks on the committed HEAD (BASE_SHA=PR base)
	$(GOBUILD) ./...
	GOOS=windows $(GOVET) ./...
	GOOS=darwin $(GOVET) ./...
	$(GOLANGCI_LINT) run
	$(GOLANGCI_LINT) run --config .golangci-contract.yml --new-from-rev="$(BASE_SHA)" --whole-files
	out=$$(mktemp -d) && { python3 -B scripts/security_gate.py --base "$(BASE_SHA)" --scanner "$(GOSEC)" --output "$$out/reports"; rc=$$?; rm -rf "$$out"; exit $$rc; }
	@test -d node_modules || npm ci --no-audit --no-fund
	$(MAKE) check-documentation BASE_SHA="$(BASE_SHA)"
	$(GOTEST) -race -count=1 ./...

check-code-contract: ## Check committed changed Go structure (BASE_SHA=PR base)
	python3 -B -m unittest discover -s scripts/tests -p 'test_*code_contract*.py'
	python3 -B scripts/check_code_contract.py --base "$(BASE_SHA)"

check-python-contract: ## Check committed Python complexity and test ownership (BASE_SHA=PR base)
	python3 -B -m unittest discover -s scripts/tests -p 'test_*python_contract*.py' -v
	python3 -B -m scripts.check_python_contract --base "$(BASE_SHA)"

check-code-quality: check-code-contract ## Run pinned contract linter after installation
	golangci-lint run --config .golangci-contract.yml --new-from-rev="$(BASE_SHA)" --whole-files


check-documentation: ## Check committed documentation (BASE_SHA=PR base)
	node --test scripts/tests/docs_contract.test.mjs
	node scripts/check_docs.mjs --base "$(BASE_SHA)"


#
# Dependencies
#

deps: ## Download dependencies
	$(GOMOD) download

deps-tidy: ## Tidy dependencies
	$(GOMOD) tidy

install-tools: ## Install development tools
	@echo "Installing development tools..."
	@go install golang.org/x/tools/cmd/deadcode@latest

#
# Git Worktree Helpers
#

worktree-list: ## List all worktrees
	@git worktree list

worktree-new: ## Create new feature worktree (NAME=feature-name)
	@echo "Creating worktree for feature/$(NAME)..."
	@cd .. && git worktree add -b feature/$(NAME) $(NAME) next
	@echo "Worktree created at ../$(NAME)"
	@echo "Run: cd ../$(NAME)"

worktree-remove: ## Remove feature worktree (NAME=feature-name)
	@echo "Removing worktree $(NAME)..."
	@cd .. && git worktree remove $(NAME) 2>/dev/null || true
	@cd .. && git branch -d feature/$(NAME) 2>/dev/null || true
	@echo "Worktree removed"

#
# Development
#

session-start: admission-runner-start ## Start development session; fail when admission runner cannot start
	@echo "FlowState Development Session"
	@echo "============================="
	@echo ""
	@echo "Worktree: $$(pwd)"
	@echo "Branch: $$(git branch --show-current)"
	@echo ""
	@go version
	@echo ""
	@if command -v ollama &> /dev/null; then \
		echo "Ollama: installed"; \
	else \
		echo "Ollama: not installed"; \
	fi
	@echo ""
	

#
# AI Attribution
#

ai-commit: ## Commit with external metadata (FILE=message RECORD=fetched-cache)
	@bash scripts/ai-commit.sh "$(FILE)" "$(RECORD)"

check-ai-attribution: ## Validate latest neutral metadata reference
	@python3 scripts/project_metadata.py --changed --base HEAD~1

list-ai-commits: ## List commits carrying neutral external metadata references
	@git log --all --grep="^Project-Metadata:" --oneline

#
# Task Management
#

new-task: ## Show external task-record policy
	@echo "Create task packets in Dropbox /FlowState/Metadata/attempts, outside this checkout."

list-tasks: ## Show external task-record location
	@echo "Read current task pointers in Dropbox /FlowState/Metadata/ACTIVE.json."

#
# Help
#

help: ## Show this help
	@echo "FlowState - AI Assistant TUI"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
