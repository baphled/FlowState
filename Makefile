.PHONY: all build run test fmt lint check clean help ai-commit check-ai-attribution list-ai-commits install-tools check-pr-size check-size-live install-hooks

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

check-test-contract: ## Check changed tests (BASE_SHA=exact PR base)
	python3 -B -m unittest discover -s scripts/tests -p test_test_contract.py
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

check: fmt lint test ## Run all checks

check-code-contract: ## Check committed changed Go structure (BASE_SHA=PR base)
	python3 -B -m unittest discover -s scripts/tests -p test_code_contract.py
	python3 -B scripts/check_code_contract.py --base "$(BASE_SHA)"

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

session-start: ## Start development session
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
