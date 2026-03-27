# Executor

The Executor is responsible for consuming a plan produced by the [Planning Loop](./PLANNING_LOOP.md) and driving task-by-task execution. This document describes the plan format the executor reads, the expanded fields added in the deterministic planning loop feature, and how backward compatibility is maintained.

## Plan Format

Plans are stored as `plan.File` values, serialised to markdown files with YAML frontmatter. The executor reads the structured fields from the frontmatter and the markdown body for task descriptions.

### Core fields (always present)

| Field         | JSON / YAML key | Type          | Description                                           |
|---------------|-----------------|---------------|-------------------------------------------------------|
| `ID`          | `id`            | string        | Unique plan identifier (typically the chain ID)       |
| `Title`       | `title`         | string        | Short human-readable title                            |
| `Description` | `description`   | string        | Plan summary                                          |
| `Status`      | `status`        | string        | `"approved"`, `"draft"`, `"executing"`, `"done"`     |
| `CreatedAt`   | `created_at`    | `time.Time`   | Creation timestamp                                    |
| `Tasks`       | `tasks`         | `[]Task`      | Ordered list of tasks to execute                      |

### Expanded fields (optional, zero-value safe)

These fields were added by the deterministic planning loop feature. They are all tagged `omitempty` — the executor works correctly when any or all of them are absent. Existing plans without these fields continue to function without modification.

| Field                 | JSON / YAML key         | Type              | Description                                              |
|-----------------------|-------------------------|-------------------|----------------------------------------------------------|
| `TLDR`                | `tldr`                  | string            | One-paragraph summary of the plan                       |
| `Context`             | `context`               | `SourceContext`   | Source material that shaped the plan (see below)        |
| `WorkObjectives`      | `work_objectives`       | `WorkObjectives`  | Desired outcome, deliverables, and scope (see below)    |
| `VerificationStrategy`| `verification_strategy` | string            | How completion will be verified                          |
| `Reviews`             | `reviews`               | `[]ReviewResult`  | History of review verdicts from the planning loop        |
| `ValidationStatus`    | `validation_status`     | string            | Harness validation outcome                              |
| `AttemptCount`        | `attempt_count`         | int               | Number of write attempts made by the Plan Writer        |
| `Score`               | `score`                 | float64           | Harness quality score (0.0 – 1.0)                       |
| `ValidationErrors`    | `validation_errors`     | `[]string`        | Errors from the harness validator                        |

#### SourceContext

Captures the research material used when writing the plan.

| Field              | JSON key             | Description                                              |
|--------------------|----------------------|----------------------------------------------------------|
| `OriginalRequest`  | `original_request`   | The user's original request text                         |
| `InterviewSummary` | `interview_summary`  | Summary from the requirements interview                  |
| `ResearchFindings` | `research_findings`  | Evidence dossier from the Analyst                        |

#### WorkObjectives

Captures the desired outcome and scope boundaries.

| Field             | JSON key            | Description                                              |
|-------------------|---------------------|----------------------------------------------------------|
| `CoreObjective`   | `core_objective`    | The single primary goal                                  |
| `Deliverables`    | `deliverables`      | List of expected outputs                                 |
| `DefinitionOfDone`| `definition_of_done`| Conditions that mark the plan complete                  |
| `MustHave`        | `must_have`         | Non-negotiable requirements                              |
| `MustNotHave`     | `must_not_have`     | Explicit exclusions and out-of-scope items               |

#### ReviewResult

One entry per review cycle in `Reviews`.

| Field            | JSON key          | Description                                              |
|------------------|-------------------|----------------------------------------------------------|
| `Verdict`        | `verdict`         | `"APPROVE"` or `"REJECT"`                               |
| `Confidence`     | `confidence`      | Reviewer's confidence (0.0 – 1.0)                       |
| `BlockingIssues` | `blocking_issues` | Issues that caused a rejection                           |
| `Suggestions`    | `suggestions`     | Non-blocking improvement suggestions                     |

### Task fields

Each `Task` in the `Tasks` slice:

| Field                | JSON / YAML key       | Type       | Description                                         |
|----------------------|-----------------------|------------|-----------------------------------------------------|
| `Title`              | `title`               | string     | Short task name                                     |
| `Description`        | `description`         | string     | Full task description                               |
| `Status`             | `status`              | string     | `"pending"`, `"in_progress"`, `"done"`, `"skipped"` |
| `AcceptanceCriteria` | `acceptance_criteria` | `[]string` | Conditions that verify task completion              |
| `Skills`             | `skills`              | `[]string` | Skill names to load for this task                   |
| `Category`           | `category`            | string     | Task category for grouping                          |
| `FileChanges`        | `file_changes`        | `[]string` | Files expected to change                            |
| `Evidence`           | `evidence`            | string     | Link to evidence confirming completion              |
| `Dependencies`       | `dependencies`        | `[]string` | Task titles that must complete first                |
| `EstimatedEffort`    | `estimated_effort`    | string     | Effort estimate (e.g. `"30m"`, `"2h"`)             |
| `Wave`               | `wave`                | int        | Execution wave number for parallel task groups      |

## Backward Compatibility

The executor does not depend on any of the expanded fields. All new fields are tagged `omitempty` in both JSON and YAML:

```go
TLDR                 string         `json:"tldr,omitempty" yaml:"tldr,omitempty"`
Context              SourceContext  `json:"context" yaml:"context"`
WorkObjectives       WorkObjectives `json:"work_objectives" yaml:"work_objectives"`
VerificationStrategy string         `json:"verification_strategy,omitempty" yaml:"verification_strategy,omitempty"`
Reviews              []ReviewResult `json:"reviews,omitempty" yaml:"reviews,omitempty"`
ValidationStatus     string         `json:"validation_status,omitempty" yaml:"validation_status,omitempty"`
AttemptCount         int            `json:"attempt_count,omitempty" yaml:"attempt_count,omitempty"`
Score                float64        `json:"score,omitempty" yaml:"score,omitempty"`
ValidationErrors     []string       `json:"validation_errors,omitempty" yaml:"validation_errors,omitempty"`
```

A plan with only `id`, `title`, `status`, `created_at`, and `tasks` is valid. The executor reads `Tasks` to drive execution — it does not require `TLDR`, `Context`, `WorkObjectives`, or any review history.

## Example Plan File

```yaml
---
id: chain-abc123
title: "Implement OAuth2 integration"
description: "Add GitHub and Google OAuth2 providers to the authentication layer"
status: approved
created_at: 2026-03-27T14:30:00Z
tldr: "Add OAuth2 support for GitHub and Google by integrating golang.org/x/oauth2, updating the session middleware, and adding provider-specific callback handlers."
context:
  original_request: "We need social login. Start with GitHub, then Google."
  interview_summary: "Users expect single-click login. JWT sessions. Callback URLs must be configurable."
  research_findings: "golang.org/x/oauth2 is the standard library. State parameter is required for CSRF."
work_objectives:
  core_objective: "Provide OAuth2 login for GitHub and Google accounts"
  deliverables:
    - "OAuthProvider interface"
    - "GitHub and Google implementations"
    - "Callback handler middleware"
  definition_of_done:
    - "Users can log in with GitHub"
    - "Users can log in with Google"
    - "All existing auth tests still pass"
  must_have:
    - "CSRF state parameter"
    - "Configurable callback URLs"
  must_not_have:
    - "Storing OAuth tokens in the database"
verification_strategy: "Run OAuth integration tests against mocked provider endpoints"
reviews:
  - verdict: APPROVE
    confidence: 0.91
    blocking_issues: []
    suggestions:
      - "Consider adding a token refresh mechanism in a follow-up"
tasks:
  - title: "Define OAuthProvider interface"
    description: "Create the interface in internal/auth/ with Authenticate and Callback methods"
    status: pending
    acceptance_criteria:
      - "Interface compiles"
      - "Both GitHub and Google implement it"
    skills:
      - "golang"
      - "clean-code"
    category: "interface"
    wave: 1
---
```

## Related Documents

- [PLANNING_LOOP.md](./PLANNING_LOOP.md) — how the plan is produced before execution
- [RESEARCH_AGENTS.md](./RESEARCH_AGENTS.md) — how Context and WorkObjectives are populated
- [EVENTS.md](./EVENTS.md) — PlanArtifactEvent and ReviewVerdictEvent schemas
