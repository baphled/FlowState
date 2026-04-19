#!/bin/bash

set -e

# ============================================================================
# AI Commit Helper
# ============================================================================
# Automates AI-attributed commits by:
# 1. Reading commit message from a file
# 2. Validating commit message format
# 3. Checking for staged changes
# 4. Adding AI attribution and human review trailers
# 5. Creating the commit
#
# Usage: 
#   make ai-commit FILE=/path/to/commit-msg.txt
#   make ai-commit FILE=/path/to/commit-msg.txt NO_VERIFY=1
#
# Environment variables for attribution override:
#   AI_AGENT - Override the AI agent name (auto-detected from OPENCODE env)
#   AI_MODEL - Override the model name (required if not auto-detected)
# ============================================================================

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Get file path from first argument
COMMIT_FILE="$1"

# Check if NO_VERIFY flag is set (passed as second argument)
NO_VERIFY="${2:-}"

# ============================================================================
# Step 1: Validate file provided and read commit message
# ============================================================================

if [ -z "$COMMIT_FILE" ]; then
    echo -e "${RED}ERROR: Commit message file required${NC}"
    echo ""
    echo "Usage:"
    echo "  make ai-commit FILE=/path/to/commit-msg.txt"
    echo ""
    echo "Create your commit message file first:"
    echo ""
    echo "  cat > /tmp/commit.txt << 'EOF'"
    echo "  feat(scope): short description"
    echo ""
    echo "  Optional longer explanation..."
    echo "  EOF"
    echo ""
    echo "  make ai-commit FILE=/tmp/commit.txt"
    echo ""
    exit 1
fi

# Check if file exists and is readable
if [ ! -f "$COMMIT_FILE" ]; then
    echo -e "${RED}ERROR: File not found: ${COMMIT_FILE}${NC}"
    echo ""
    echo "Create the file first:"
    echo "  cat > ${COMMIT_FILE} << 'EOF'"
    echo "  feat(scope): description"
    echo "  EOF"
    echo ""
    exit 1
fi

if [ ! -r "$COMMIT_FILE" ]; then
    echo -e "${RED}ERROR: Cannot read file: ${COMMIT_FILE}${NC}"
    exit 1
fi

echo -e "${BLUE}Reading commit message from: ${COMMIT_FILE}${NC}"
COMMIT_MSG=$(cat "$COMMIT_FILE")

# Validate we have a message
if [ -z "$COMMIT_MSG" ]; then
    echo -e "${RED}ERROR: Commit message file is empty${NC}"
    exit 1
fi

# Validate message is not a placeholder
if [[ "$COMMIT_MSG" =~ ^\.\.\.$ ]] || [[ "$COMMIT_MSG" =~ ^\.\.\.\s*$ ]] || [[ "$COMMIT_MSG" == "..." ]]; then
    echo -e "${RED}ERROR: Commit message cannot be '...' placeholder${NC}"
    echo ""
    echo "Edit your file with an actual commit message:"
    echo "  ${COMMIT_FILE}"
    echo ""
    exit 1
fi

# Validate message has actual content (not just type prefix)
# Get first line and strip whitespace
FIRST_LINE=$(echo "$COMMIT_MSG" | head -n1 | sed 's/[[:space:]]*$//')
if [[ "$FIRST_LINE" =~ ^[a-z]+\([a-zA-Z0-9_-]+\):$ ]] || [[ "$FIRST_LINE" =~ ^[a-z]+:$ ]]; then
    echo -e "${RED}ERROR: Commit message has no description${NC}"
    echo ""
    echo "Edit your file to add a description after the colon:"
    echo "  ${COMMIT_FILE}"
    echo ""
    exit 1
fi

# ============================================================================
# Step 2: Check for staged changes
# ============================================================================

echo ""
echo -e "${BLUE}Checking for staged changes...${NC}"

if git diff --cached --quiet; then
    echo -e "${RED}ERROR: No staged changes${NC}"
    echo ""
    echo "You must stage changes before committing:"
    echo "  git add -p <file>          # Stage specific hunks interactively"
    echo "  git add <file>             # Stage entire file"
    echo ""
    echo "Then try again:"
    echo "  make ai-commit FILE=${COMMIT_FILE}"
    echo ""
    exit 1
fi

echo -e "${GREEN}Staged changes detected${NC}"

# ============================================================================
# Step 3: Validate commit message format
# ============================================================================

echo ""
echo -e "${BLUE}Validating commit message format...${NC}"

# Basic conventional commit format check
if ! echo "$FIRST_LINE" | grep -qE "^(feat|fix|docs|style|refactor|test|chore|perf|ci|build|revert)(\([a-zA-Z0-9_-]+\))?: .+"; then
    echo -e "${YELLOW}Warning: Message may not follow conventional commit format${NC}"
    echo ""
    echo "Recommended format:"
    echo -e "${GREEN}  type(scope): subject${NC}"
    echo ""
    echo "Examples:"
    echo "  feat(chat): add streaming response"
    echo "  fix(nav): resolve scroll issue"
    echo "  docs(readme): update installation"
    echo ""
else
    echo -e "${GREEN}Commit message format valid${NC}"
fi

# ============================================================================
# Step 4: Detect AI agent and model
# ============================================================================

# Auto-detect AI agent from environment
detect_ai_agent() {
    # Check for explicit override first
    if [ -n "$AI_AGENT" ]; then
        echo "$AI_AGENT"
        return
    fi
    
    # Detect Opencode (primary check)
    if [ "$OPENCODE" = "1" ] || [ -n "$OPENCODE" ]; then
        echo "Opencode"
        return
    fi
    
    # Detect Claude Code
    if [ -n "$CLAUDE_CODE" ]; then
        echo "Claude Code"
        return
    fi
    
    # Detect Cursor
    if [ -n "$CURSOR_SESSION" ] || [ -n "$CURSOR" ]; then
        echo "Cursor"
        return
    fi
    
    # No agent detected
    echo ""
}

# Detect model - REQUIRED, no defaults
detect_ai_model() {
    # Check for explicit override first
    if [ -n "$AI_MODEL" ]; then
        echo "$AI_MODEL"
        return
    fi
    
    # No model detected
    echo ""
}

AGENT_NAME=$(detect_ai_agent)
MODEL_NAME=$(detect_ai_model)

# Validate agent detected
if [ -z "$AGENT_NAME" ]; then
    echo -e "${RED}ERROR: Could not detect AI agent${NC}"
    echo ""
    echo "Set the AI_AGENT environment variable:"
    echo "  export AI_AGENT='Opencode'"
    echo ""
    echo "Or run with:"
    echo "  AI_AGENT='Opencode' AI_MODEL='claude-opus-4-5' make ai-commit FILE=/tmp/commit.txt"
    echo ""
    exit 1
fi

# Validate model - REQUIRED
if [ -z "$MODEL_NAME" ]; then
    echo -e "${RED}ERROR: AI_MODEL environment variable not set${NC}"
    echo ""
    echo "The model must be specified for accurate attribution."
    echo ""
    echo "Set the AI_MODEL environment variable:"
    echo "  export AI_MODEL='claude-opus-4-5'"
    echo ""
    echo "Or run with:"
    echo "  AI_MODEL='claude-opus-4-5' make ai-commit FILE=/tmp/commit.txt"
    echo ""
    echo "Common models:"
    echo "  claude-opus-4-5, claude-sonnet-4, gpt-4o, llama3.2"
    echo ""
    exit 1
fi

# Get reviewer name from git config
REVIEWER_NAME=$(git config user.name)

if [ -z "$REVIEWER_NAME" ]; then
    echo -e "${YELLOW}Warning: git user.name not set${NC}"
    echo "Set it with: git config user.name \"Your Name\""
    REVIEWER_NAME="Unknown"
fi

# ============================================================================
# Step 4b: Guard 2 — Behaviour-Pinned trailer required when a func+test
# pair lands in the same commit. See scripts/check-behaviour-pinned.sh
# for the full rationale.
# ============================================================================

SCRIPT_DIR_FOR_GUARD="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BEHAVIOUR_PINNED_GUARD="${SCRIPT_DIR_FOR_GUARD}/check-behaviour-pinned.sh"

if [ -x "$BEHAVIOUR_PINNED_GUARD" ]; then
    echo ""
    echo -e "${BLUE}Checking Behaviour-Pinned trailer (Guard 2)...${NC}"
    if ! "$BEHAVIOUR_PINNED_GUARD" --message-file "$COMMIT_FILE"; then
        echo -e "${RED}Guard 2 blocked the commit. See message above.${NC}"
        exit 1
    fi
    echo -e "${GREEN}Guard 2 passed${NC}"
fi

# ============================================================================
# Step 5: Create commit with AI attribution
# ============================================================================

echo ""
echo -e "${BLUE}Creating AI-attributed commit...${NC}"
echo ""
echo "Agent:    ${AGENT_NAME}"
echo "Model:    ${MODEL_NAME}"
echo "Reviewer: ${REVIEWER_NAME}"
echo ""

# Build full commit message with attribution
FINAL_MSG_FILE=$(mktemp)

cat > "$FINAL_MSG_FILE" << EOF
${COMMIT_MSG}

AI-Generated-By: ${AGENT_NAME} (${MODEL_NAME})
Reviewed-By: ${REVIEWER_NAME}
EOF

# Create the commit using the temp file
COMMIT_FLAGS="-F $FINAL_MSG_FILE"
if [ "$NO_VERIFY" = "1" ]; then
    echo -e "${YELLOW}Skipping pre-commit hooks (--no-verify)${NC}"
    echo ""
    COMMIT_FLAGS="$COMMIT_FLAGS --no-verify"
fi

if git commit $COMMIT_FLAGS; then
    echo ""
    echo -e "${GREEN}Commit created successfully${NC}"
    echo ""
    echo "Commit message:"
    echo "---------------------------------------------"
    git log -1 --pretty=%B
    echo "---------------------------------------------"
    echo ""
    
    rm -f "$FINAL_MSG_FILE"
else
    echo ""
    echo -e "${RED}Commit failed${NC}"
    rm -f "$FINAL_MSG_FILE"
    exit 1
fi

# ============================================================================
# Step 6: Summary
# ============================================================================

echo -e "${GREEN}AI-attributed commit complete${NC}"
echo ""
echo "Next steps:"
echo "  git log -1         # Review the commit"
echo "  make check         # Run checks"
echo "  git push           # Push to remote (when ready)"
echo ""
