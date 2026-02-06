# Task 002: Basic TUI Shell

**Status:** Not Started
**Priority:** High
**Depends On:** 001-foundation-setup

## Objective

Create a minimal TUI shell that can display and accept input, following BDD workflow.

## Target Scenario

```gherkin
@smoke
Scenario: Send message and receive streaming response
  Given FlowState is running
  When I type "What is 2 + 2?"
  And I press Enter
  Then I should see tokens appearing
  And I should see a complete response
```

## Deliverables

- [ ] BubbleTea app structure (`internal/tui/app/`)
- [ ] Basic chat intent (`internal/tui/intents/chat/`)
- [ ] Input component with mode switching
- [ ] Message viewport
- [ ] Provider interface (`internal/provider/provider.go`)
- [ ] Ollama provider with streaming (`internal/provider/ollama/`)
- [ ] Godog step definitions

## Implementation Steps

### Step 1: App Shell (RED -> GREEN)
```gherkin
Given FlowState is running
```
- Create minimal `cmd/flowstate/main.go`
- Create `internal/tui/app/app.go` with BubbleTea model
- Create step definition to start app

### Step 2: Input Handling (RED -> GREEN)
```gherkin
When I type "What is 2 + 2?"
```
- Add text input component
- Handle insert mode
- Create step to send keystrokes

### Step 3: Submit Message (RED -> GREEN)
```gherkin
And I press Enter
```
- Handle Enter key
- Submit message to provider

### Step 4: Streaming Response (RED -> GREEN)
```gherkin
Then I should see tokens appearing
```
- Implement provider interface
- Create Ollama provider with streaming
- Display tokens as they arrive

### Step 5: Complete Response (GREEN)
```gherkin
And I should see a complete response
```
- Verify full response displayed
- Refactor as needed

## Technical Notes

### Provider Interface

```go
type Provider interface {
    Name() string
    Models() ([]Model, error)
    Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)
}
```

### App Structure

```go
type App struct {
    intent   Intent
    width    int
    height   int
    provider provider.Provider
}
```

### Chat Intent

```go
type ChatIntent struct {
    messages []Message
    input    textinput.Model
    viewport viewport.Model
    provider provider.Provider
    mode     Mode
}
```

## Acceptance Criteria

- [ ] `make bdd-smoke` passes
- [ ] Can type a message
- [ ] Can see streaming response
- [ ] Mode indicator shows current mode
- [ ] Ctrl+C quits cleanly

## Files to Create

```
cmd/flowstate/main.go
internal/tui/app/app.go
internal/tui/intents/chat/intent.go
internal/tui/intents/chat/messages.go
internal/provider/provider.go
internal/provider/ollama/client.go
internal/provider/ollama/provider.go
features/steps/common_steps.go
features/steps/chat_steps.go
```

## Testing

```bash
# Watch the scenario fail first
make bdd-smoke

# Then implement step by step
make bdd-wip
```
