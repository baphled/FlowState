# BDD Workflow

FlowState follows strict BDD (Behavior-Driven Development) practices using Godog/Cucumber.

## The Golden Rule

**Write the Cucumber scenario FIRST, before any implementation.**

## Outside-In Development

```
1. Write Cucumber scenario    -> Acceptance test (what user sees)
2. Watch it fail              -> Verify correct failure
3. Smallest possible change   -> Just enough to pass ONE step
4. Run scenario               -> See next failure
5. Repeat                     -> Until scenario passes
6. Refactor                   -> Clean up while green
```

## Scenario Structure

```gherkin
Feature: Basic Chat
  As a user
  I want to chat with an AI assistant
  So that I can get help with tasks

  @smoke
  Scenario: Send message and receive streaming response
    Given FlowState is running
    When I type "What is 2 + 2?"
    And I press Enter
    Then I should see tokens appearing
    And I should see "4" in the response
```

## Tags

| Tag | Purpose | When to Run |
|-----|---------|-------------|
| `@smoke` | Critical path tests | Every commit |
| `@wip` | Work in progress | During development |
| `@chat` | Chat functionality | Feature-specific |
| `@navigation` | Vim navigation | Feature-specific |
| `@sessions` | Session management | Feature-specific |
| `@tools` | Tool system | Feature-specific |

## Running Tests

```bash
make bdd           # All BDD tests
make bdd-smoke     # Smoke tests only
make bdd-wip       # WIP tests only
make bdd TAGS=@chat # Specific tag
```

## Step Definitions

Step definitions live in `features/steps/`. Each feature area has its own step file:

```
features/
├── chat/
│   └── basic_chat.feature
├── navigation/
│   └── vim_motions.feature
└── steps/
    ├── chat_steps.go
    ├── navigation_steps.go
    └── common_steps.go
```

## The Smallest Change

When implementing steps, make the **smallest possible change**:

| Situation | Smallest Change | NOT Smallest Change |
|-----------|-----------------|---------------------|
| Need a function | Return nil/zero value | Full implementation |
| Need a struct | One required field | All possible fields |
| Need validation | One check | All validations |
| Need UI element | Minimal render | Full styled component |

## Commit Cadence

Commit at each meaningful step:

```
feat(chat): add scenario for basic chat [RED]
feat(chat): create app struct [GREEN step 1]
feat(chat): add input handling [GREEN step 2]
feat(chat): implement streaming [GREEN step 3]
feat(chat): display response [GREEN all steps]
refactor(chat): extract message formatting [REFACTOR]
```

## Anti-Patterns

**Never:**
- Implement features without a failing scenario first
- Write multiple features before testing
- Skip the "watch it fail" step
- Make large changes to pass a step

**Always:**
- One scenario at a time
- One step at a time
- Commit after each green step
- Refactor only when green
