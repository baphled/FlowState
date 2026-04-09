.PHONY: all build run test test-external test-recall bdd bdd-smoke bdd-wip fmt lint check check-docblocks check-untested-packages check-note-comments clean help ai-commit check-ai-attribution list-ai-commits coverage-check install-coverage-tools install-hooks debug-session debug-latest debug-errors session-overview log-analysis parse-recording session-history session-history-detail session-ids

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

test: ## Run all Go tests (excluding BDD features)
	@echo "Running tests..."
	$(GOTEST) -v $(shell go list ./... | grep -v '/features/')

test-external: ## Run external integration tests (requires QDRANT_URL)
	@if [ -z "$(QDRANT_URL)" ]; then \
		echo "QDRANT_URL is not set. Run: QDRANT_URL=http://localhost:6333 make test-external"; \
		exit 1; \
	fi
	$(GOTEST) -v ./... --ginkgo.label-filter="external" -count=1

test-recall: ## Run recall integration tests
	$(GOTEST) -v ./internal/recall/... --ginkgo.label-filter="integration" -v -count=1

test-coverage: ## Run tests with coverage (excluding BDD features)
	@echo "Running tests with coverage..."
	$(GOTEST) -v -coverprofile=coverage.out $(shell go list ./... | grep -v '/features/')
	$(GOCMD) tool cover -html=coverage.out

GOBIN ?= $$(go env GOPATH)/bin

install-coverage-tools: ## Install go-test-coverage tool
	@echo "Installing go-test-coverage..."
	go install github.com/vladopajic/go-test-coverage/v2@latest

coverage-check: ## Check test coverage against thresholds (excluding BDD features)
	@echo "Running coverage check..."
	@$(GOTEST) $(shell go list ./... | grep -v '/features/') -coverprofile=./coverage.out -covermode=atomic -coverpkg=./... 2>/dev/null
	@$(GOBIN)/go-test-coverage --config=./.testcoverage.yml

#
# BDD Testing (Godog/Cucumber)
#

bdd: ## Run all BDD tests
	@echo "Running BDD tests..."
	go test -v ./features/...

bdd-smoke: ## Run smoke BDD tests
	@echo "Running smoke tests..."
	GODOG_TAGS="@smoke" go test -v ./features/... -run "Test"

bdd-wip: ## Run WIP BDD tests
	@echo "Running WIP tests..."
	go test -v ./features/... -run "Test"

bdd-feature: ## Run specific feature (FEATURE=chat/basic_chat)
	@echo "Running feature: $(FEATURE)"
	go test -v ./features/... -run "Test"

#
# Code Quality
#

fmt: ## Format code
	@echo "Formatting code..."
	$(GOFMT) ./...

lint: ## Run linters
	@echo "Running linters..."
	$(GOVET) ./...
	@if command -v staticcheck &> /dev/null; then staticcheck ./...; fi
	@if command -v golangci-lint &> /dev/null; then golangci-lint run; fi

check-docblocks: ## Run structured docblock analyser
	@echo "Checking docblocks..."
	@go run ./cmd/docblocks/... ./...

check-untested-packages: ## Fail if any internal/ package has no test files
	@echo "Checking for untested internal packages..."
	@FAILED=0; \
	for pkg in $$(go list ./internal/...); do \
		dir=$$(go list -f '{{.Dir}}' $$pkg); \
		if ! ls $$dir/*_test.go > /dev/null 2>&1; then \
			echo "  MISSING TESTS: $$pkg"; \
			FAILED=1; \
		fi; \
	done; \
	if [ $$FAILED -eq 1 ]; then \
		echo ""; \
		echo "ERROR: Some internal packages have no test files."; \
		echo "Add at least one *_test.go file to each package above."; \
		exit 1; \
	fi
	@echo "All internal packages have test files."

check-note-comments: ## Fail if NOTE: appears outside a docblock
	@echo "Checking NOTE: comment placement..."
	@bash scripts/check-note-comments.sh

check: build fmt lint test coverage-check check-docblocks check-untested-packages check-note-comments ## Run all checks

#
# Dependencies
#

deps: ## Download dependencies
	$(GOMOD) download

deps-tidy: ## Tidy dependencies
	$(GOMOD) tidy

#
# Hooks
#

install-hooks: ## Install git hooks (run once after checkout)
	@git config core.hooksPath .git-hooks
	@echo "Git hooks installed. Hooks directory: .git-hooks/"

#
# Git Worktree Helpers
#

worktree-list: ## List all worktrees
	@git worktree list

worktree-new: ## Create new feature worktree (NAME=feature-name)
	@echo "Creating worktree for feature/$(NAME)..."
	@cd .. && git worktree add -b feature/$(NAME) $(NAME) main
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
	@if command -v godog &> /dev/null; then \
		echo "Godog: installed"; \
	else \
		echo "Godog: not installed - run: go install github.com/cucumber/godog/cmd/godog@latest"; \
	fi
	@echo ""
	@echo "Ready! See docs/PLAN.md for tasks."

#
# AI Attribution
#

ai-commit: ## Create AI-attributed commit (FILE=/path/to/msg.txt AI_MODEL=model)
	@if [ -z "$(FILE)" ]; then \
		echo "Usage:"; \
		echo "  AI_MODEL=claude-opus-4-5 make ai-commit FILE=/path/to/commit-msg.txt"; \
		echo ""; \
		echo "Required: AI_MODEL must be set (OPENCODE=1 is set automatically)"; \
		echo ""; \
		echo "Create your commit message file:"; \
		echo "  cat > /tmp/commit.txt << 'EOF'"; \
		echo "  feat(scope): short description"; \
		echo "  "; \
		echo "  Optional longer explanation..."; \
		echo "  EOF"; \
		echo ""; \
		echo "  AI_MODEL=claude-opus-4-5 make ai-commit FILE=/tmp/commit.txt"; \
		echo ""; \
		exit 1; \
	fi
	@OPENCODE=1 bash scripts/ai-commit.sh "$(FILE)" "$(NO_VERIFY)"

check-ai-attribution: ## Check latest commit for AI attribution
	@echo "Checking latest commit for AI attribution..."
	@git log -1 --pretty=%B | grep "AI-Generated-By:" || \
		echo "Warning: No AI attribution found in latest commit"

list-ai-commits: ## List all AI-generated commits
	@echo "AI-Generated Commits:"
	@git log --all --grep="AI-Generated-By:" --oneline

#
# Task Management
#

new-task: ## Create a new task (TASK="task description")
	@echo "# Task: $(TASK)" > tasks/$$(date +%Y%m%d)-$$(echo "$(TASK)" | tr ' ' '-' | tr '[:upper:]' '[:lower:]' | head -c 30).md
	@echo "" >> tasks/$$(date +%Y%m%d)-*.md
	@echo "Created: $$(date)" >> tasks/$$(date +%Y%m%d)-*.md
	@echo "Status: pending" >> tasks/$$(date +%Y%m%d)-*.md
	@echo "Task created!"

list-tasks: ## List all tasks
	@echo "Tasks:"
	@for f in tasks/*.md; do \
		if [ -f "$$f" ]; then \
			name=$$(basename "$$f" .md); \
			status=$$(grep -m1 "Status:" "$$f" 2>/dev/null | cut -d: -f2 | tr -d ' '); \
			echo "[$${status:-unknown}] $$name"; \
		fi \
	done

#
# Debug & Analysis
#

debug-session: ## Debug a session (ID=<session-id>)
	@if [ -z "$(ID)" ]; then \
		echo "Usage: make debug-session ID=<session-uuid>"; \
		echo ""; \
		echo "Add --include-logs with: make debug-session ID=<uuid> OPTS=--include-logs"; \
		exit 1; \
	fi
	@python3 scripts/correlate-debug.py "$(ID)" $(OPTS)

debug-latest: ## Debug the most recent session
	@python3 scripts/correlate-debug.py --latest --include-logs

debug-errors: ## Find sessions that encountered errors
	@python3 scripts/correlate-debug.py --errors

session-overview: ## Show overview of all sessions (OPTS for filters)
	@python3 scripts/session-overview.py $(OPTS)

log-analysis: ## Analyse application logs (OPTS for filters)
	@python3 scripts/log-analysis.py $(OPTS)

parse-recording: ## Parse a session recording timeline (ID=<session-id>)
	@if [ -z "$(ID)" ]; then \
		echo "Usage: make parse-recording ID=<session-uuid>"; \
		echo ""; \
		echo "Flags via OPTS: make parse-recording ID=<uuid> OPTS='--tools-only'"; \
		exit 1; \
	fi
	@python3 scripts/parse-recording.py "$(ID)" $(OPTS)

session-history: ## Show session conversation history (ID=<session-id>)
	@if [ -z "$(ID)" ]; then \
		echo "Usage: make session-history ID=<session-uuid>"; \
		echo ""; \
		echo "Short view (default). For detailed: make session-history-detail ID=<uuid>"; \
		echo "For latest session: make session-history OPTS=--latest"; \
		exit 1; \
	fi
	@python3 scripts/session-history.py "$(ID)" $(OPTS)

session-history-detail: ## Show detailed session history with full content (ID=<session-id>)
	@if [ -z "$(ID)" ]; then \
		echo "Usage: make session-history-detail ID=<session-uuid>"; \
		exit 1; \
	fi
	@python3 scripts/session-history.py "$(ID)" --detail $(OPTS)

session-ids: ## Extract all IDs from a session (ID=<session-id>)
	@if [ -z "$(ID)" ]; then \
		echo "Usage: make session-ids ID=<session-uuid>"; \
		exit 1; \
	fi
	@python3 scripts/session-history.py "$(ID)" --ids-only

#
# Help
#

help: ## Show this help
	@echo "FlowState - AI Assistant TUI"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
