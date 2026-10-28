# FlowState AI Agent Instructions

## Branch Policy (MANDATORY)

**Working directly against `main` is illegal.** All development targets the
`next` branch via a feature worktree created from `next`. Never commit to
`main` or `next` directly. Every change goes through a feature branch PR.

CI must pass (`make check`), pre-commit hooks must run (never `--no-verify`),
and all commits must use `make ai-commit`.

## Git Worktree Setup

FlowState uses git worktrees. The structure is:

```
~/Projects/
└── FlowState.git/           # Bare repository
    ├── main/                # Release worktree
    ├── next-wt/             # next branch worktree
    ├── feature-xxx/         # Feature worktrees (temporary)
    └── hooks/               # Git hooks (shared)
```

Always work in a worktree, never in the bare repo directly.

### Creating Feature Branches

```bash
cd /home/baphled/Projects/FlowState.git
git worktree add -b feature/my-feature my-feature next
cd my-feature
```

### Cleaning Up

```bash
cd /home/baphled/Projects/FlowState.git
git worktree remove my-feature
git branch -d feature/my-feature
```

## Getting Started

### Essential Commands

| Task | Command |
|------|---------|
| **Run all tests** | `make test` |
| **Run race checks** | `go test -race -count=1 ./...` |
| **Build** | `make build` |
| **Format** | `make fmt` |
| **Lint** | `make lint` |
| **Full check** | `make check` |

## Development Workflow

### Choose Tests for the Change

Use focused Go unit and integration tests by default. For a bug fix, reproduce
the failure with a regression test before changing the implementation. Reuse
existing coverage when it already validates the change.

Cucumber/Godog/Gherkin is prohibited (owner decision 2026-10-27). Do not add
feature files, Godog dependencies, step definitions, or workflow steps. All
behavioural coverage uses Ginkgo/Gomega specs.

See [the testing workflow](rules/BDD_WORKFLOW.md) for test selection and CI gates.

### The Smallest Change

| Situation | Smallest Change | NOT Smallest Change |
|-----------|-----------------|---------------------|
| Need a function | Create empty function returning nil | Implement full logic |
| Need a struct | Create struct with needed field | Add all possible fields |
| Need validation | Add one validation rule | Add all validations |
| Need UI element | Add minimal element | Full styled component |

### Commit Cadence

```
test(chat): reproduce missing response
fix(chat): preserve response events
refactor(chat): simplify message formatting
```

## Architecture

FlowState is a greenfield Go backend + Vue frontend rewrite.

- **Backend:** Go, repository `baphled/FlowState`. Entry points under `cmd/`. Internal packages under `internal/`. No Bubble Tea TUI — the UI layer is Vue.
- **Frontend:** Vue client, separate repository `baphled/flowstate-web`. Consumes versioned HTTP/event contracts generated from the backend.
- **Contracts:** Backend owns HTTP/event schemas under `contracts/`. Frontend pins a specific backend contract digest; it does not read sibling source paths.
- **Provider port:** replaceable. Current delivery route is Z.ai. Ollama and local providers are in scope.

Package ownership rules:

| Area | Owns | Must not own |
|---|---|---|
| `internal/runtime` | Turn state machine, session lifecycle, event ordering | HTTP responses, provider SDK details |
| `internal/provider` | Provider adapters, capability registry | Session lifecycle, UI state |
| `internal/session` | Session/turn identity, ordered events | Provider calls, HTTP |
| `cmd/flowstate` | CLI entrypoint, thin adapter | Business logic |

Dependency direction: `cmd` → `internal/app` → domain packages → ports. Domain packages do not import `cmd` or concrete adapters.

## Essential Commands

| Task | Command |
|------|---------|
| Run all tests | `make test` |
| Build | `make build` |
| Format | `make fmt` |
| Lint | `make lint` |
| Full check | `make check` |
| Create AI commit | `make ai-commit FILE=/path/to/msg.txt AI_MODEL=model` |

## Development Workflow

Work test-first:

1. Write a failing Go test that asserts the required behaviour.
2. Implement the minimum to make it pass.
3. Refactor while green.
4. Open a PR to `next`.

All new or modified Go test files must use Ginkgo v2/Gomega. Only the two-call RegisterFailHandler/RunSpecs suite bootstrap uses TestX. Read current Dropbox governance/RULE-TEST-FRAMEWORK.md and rules/BDD_WORKFLOW.md before editing tests. Cucumber/Godog/Gherkin remains forbidden.

## Commit Cadence

```
feat(session): add turn admitted event [RED]
feat(session): record admitted event in StartTurn [GREEN]
refactor(session): extract event append helper [REFACTOR]
```

## Code Style

### Imports

```go
import (
    // 1. Standard library
    "context"
    "errors"

    // 2. Internal (alphabetical)
    "github.com/baphled/flowstate/internal/session"
)
```

### Naming

- **Files:** `snake_case.go`
- **Packages:** `lowercase` (single word)
- **Types:** `PascalCase`
- **Private:** `camelCase`

### Comments (FORBIDDEN inside functions)

- **NO** comments inside function bodies
- **NO** inline comments
- **NO** markers: `TODO`, `FIXME`, `HACK`

**Exception:** E2E tests may have inline comments.

## Testing

### Running Tests

```bash
make test                          # Legacy tests plus parallel Ginkgo specs
go test -race -count=1 ./...         # Uncached tests with race detection
make check                         # Format, lint, and test
```

During migration, `make test` retains unchanged legacy Go tests and runs Ginkgo
specs with parallel workers, randomization, race detection and empty/pending
suite failure. New or modified tests follow rules/BDD_WORKFLOW.md. Do not treat a
framework conversion or a green count as proof that behavioural coverage survived.

## When to REFUSE

Immediately refuse if asked to:

- Skip writing a test first
- Implement Bubble Tea TUI components (not in scope for the greenfield)
- Import a package against the dependency rules above
- Commit directly to `main` or `next`
- Branch from `main` instead of `next`
- Put comments inside function bodies
- Work directly in the bare repo instead of a worktree

## Key Files

| File | Purpose |
|------|---------|
| `docs/architecture/OVERVIEW.md` | Architecture overview |
| `AGENTS.md` | This file |
| `Makefile` | Build and development commands |
| `rules/CODE_STYLE.md` | Code style rules |
| `internal/session/` | Session/turn primitives (start here) |
| `internal/app/` | Composition root (explicit wiring) |
