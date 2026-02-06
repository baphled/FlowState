.PHONY: all build run test bdd bdd-smoke bdd-wip fmt lint check clean help

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

test: ## Run all Go tests
	@echo "Running tests..."
	$(GOTEST) -v ./...

test-coverage: ## Run tests with coverage
	@echo "Running tests with coverage..."
	$(GOTEST) -v -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out

#
# BDD Testing (Godog/Cucumber)
#

bdd: ## Run all BDD tests
	@echo "Running BDD tests..."
	godog run ./features/...

bdd-smoke: ## Run smoke BDD tests
	@echo "Running smoke tests..."
	godog run --tags=@smoke ./features/...

bdd-wip: ## Run WIP BDD tests
	@echo "Running WIP tests..."
	godog run --tags=@wip ./features/...

bdd-feature: ## Run specific feature (FEATURE=chat/basic_chat)
	@echo "Running feature: $(FEATURE)"
	godog run ./features/$(FEATURE).feature

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

check: fmt lint test ## Run all checks

#
# Dependencies
#

deps: ## Download dependencies
	$(GOMOD) download

deps-tidy: ## Tidy dependencies
	$(GOMOD) tidy

#
# Git Worktree Helpers
#

worktree-list: ## List all worktrees
	@cd .. && git worktree list

worktree-new: ## Create new feature worktree (NAME=feature-name)
	@echo "Creating worktree for feature/$(NAME)..."
	@cd .. && git worktree add -b feature/$(NAME) ../FlowState-$(NAME) main
	@echo "Worktree created at ../FlowState-$(NAME)"
	@echo "Run: cd ../FlowState-$(NAME)"

worktree-remove: ## Remove feature worktree (NAME=feature-name)
	@echo "Removing worktree $(NAME)..."
	@cd .. && git worktree remove ../FlowState-$(NAME) 2>/dev/null || true
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
