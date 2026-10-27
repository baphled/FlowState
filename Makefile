.PHONY: all build run test bdd bdd-smoke bdd-wip fmt lint check clean help ai-commit check-ai-attribution list-ai-commits install-tools

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

# Legacy commands intentionally fail instead of installing or running Cucumber.
bdd bdd-smoke bdd-wip bdd-feature:
	@echo "Cucumber/Godog/Gherkin execution is forbidden. Use standard Go unit/integration tests." >&2
	@exit 1

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
	@echo "Read current Dropbox /FlowState/Dev and /FlowState/Metadata/ACTIVE.json before work."

#
# AI Attribution
#

ai-commit: ## Paused until the external Dropbox commit helper is available
	@echo "The previous helper invents AI/reviewer claims. Use the corrected external-record helper before agent commits." >&2
	@exit 1

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

new-task: ## Task records belong in Dropbox Metadata
	@echo "Create the assigned packet under Dropbox /FlowState/Metadata/attempts/, outside this checkout."
	@exit 1

list-tasks: ## Find the current assigned Dropbox packet
	@echo "Read Dropbox /FlowState/Metadata/ACTIVE.json and the assigned packet."

#
# Help
#

help: ## Show this help
	@echo "FlowState - AI Assistant"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
