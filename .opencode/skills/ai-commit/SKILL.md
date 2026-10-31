---
name: ai-commit
description: Create properly attributed commits for AI-generated code
license: MIT
compatibility: opencode
metadata:
  audience: developers
  workflow: flowstate
---

## What I do

Create commits with **accurate** AI attribution. Attribution MUST be correct - no guessing.

## Critical Rules

### NEVER use `git commit` directly

Always use:
```bash
AI_MODEL=claude-opus-4-5 make ai-commit FILE=/tmp/commit.txt
```

### ALWAYS fix incorrect attribution

If a commit has wrong or missing attribution:
1. **Immediately amend** if it's the last commit
2. **Rebase and amend** if it's an older commit
3. **Never leave incorrect attribution** - it undermines trust and audit trails

### Attribution MUST be accurate

- **AI_MODEL is REQUIRED** - no defaults, no guessing
- Agent is auto-detected from `OPENCODE=1` environment variable
- Reviewer comes from `git config user.name`

## Commit Procedure

### 1. Prepare Commit Message

```bash
cat > /tmp/commit.txt << 'EOF'
type(scope): short description

Optional longer explanation of WHY the change was made.
EOF
```

### 2. Create Commit

```bash
AI_MODEL=claude-opus-4-5 make ai-commit FILE=/tmp/commit.txt
```

The script will:
1. Validate the commit message format
2. Check for staged changes
3. Auto-detect agent (Opencode from `OPENCODE=1`)
4. Require `AI_MODEL` to be explicitly set
5. Add attribution trailers
6. Create the commit

### 3. Verify Attribution

```bash
git log -1 --pretty=%B
```

Should show:
```
type(scope): description

...

AI-Generated-By: Opencode (claude-opus-4-5)
Reviewed-By: Your Name
```

## Fixing Incorrect Attribution

### Last commit (not pushed)

```bash
# Create corrected message
cat > /tmp/fix.txt << 'EOF'
original commit message here

AI-Generated-By: Opencode (claude-opus-4-5)
Reviewed-By: Your Name
EOF

git commit --amend -F /tmp/fix.txt
```

### Older commit (not pushed)

```bash
# Start interactive rebase
GIT_SEQUENCE_EDITOR="sed -i 's/^pick HASH/edit HASH/'" git rebase -i HASH^

# Amend the commit
git commit --amend -F /tmp/fix.txt

# Continue rebase
git rebase --continue
```

### Already pushed

**WARNING**: Requires force push. Coordinate with team first.

```bash
# Fix locally as above, then:
git push --force-with-lease
```

## Commit Message Format

### Types
- `feat` - New feature
- `fix` - Bug fix  
- `docs` - Documentation only
- `refactor` - Code restructuring
- `test` - Adding/updating tests
- `chore` - Maintenance tasks
- `perf` - Performance improvement
- `ci` - CI/CD changes
- `build` - Build system changes

### Scopes (FlowState)
- `chat` - Chat functionality
- `nav` - Navigation/vim motions
- `session` - Session management
- `provider` - LLM providers
- `tools` - Tool system
- `tui` - TUI components
- `build` - Build/Makefile

### Examples

```
feat(chat): add streaming response display

Implements token-by-token display of LLM responses
using a viewport component with auto-scroll.
```

```
fix(nav): correct half-page scroll calculation

The viewport was scrolling by lines instead of
half the visible height.
```

## Model Names

Use the exact model identifier:
- `claude-opus-4-5` (not "Claude Opus" or "opus")
- `claude-sonnet-4` (not "Sonnet 4")
- `gpt-4o` (not "GPT-4")
- `llama3.2` (for Ollama models)

## Pre-Commit Checklist

Before committing:

1. **Stage changes selectively:**
   ```bash
   git add -p  # Review each hunk
   ```

2. **Run checks:**
   ```bash
   make check
   ```

3. **Verify what you're committing:**
   ```bash
   git diff --cached
   ```

## Git Safety Rules

**NEVER:**
- Use `git commit` without `make ai-commit`
- Guess or default the AI model
- Leave incorrect attribution unfixed
- Skip hooks without explicit user request (`NO_VERIFY=1`)
- Force push to main/next without coordination

**ALWAYS:**
- Use explicit `AI_MODEL` environment variable
- Verify attribution after commit (`git log -1`)
- Amend immediately if attribution is wrong
