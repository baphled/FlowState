.PHONY: all build run test fmt lint check clean help ai-commit check-ai-attribution list-ai-commits install-tools

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

ai-commit: ## Create AI-attributed commit (FILE=/path/to/msg.txt AI_MODEL=model)
	@if [ -z "$(FILE)" ]; then \
		echo "Usage:"; \
		echo "  AI_MODEL=claude-opus-4-5 make ai-commit FILE=/path/to/commit-msg.txt"; \
		echo ""; \
		echo "Required: AI_MODEL must be set (agent auto-detected from OPENCODE env)"; \
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
	@bash scripts/ai-commit.sh "$(FILE)" "$(NO_VERIFY)"

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
# Help
#

help: ## Show this help
	@echo "FlowState - AI Assistant TUI"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
