---
id: dynamic-routing
name: Dynamic Routing
version: "1.0.0"
description: >
  Decision logic for the coordinator to choose which agents to engage based
  on task type. Defines routing rules, signals, and a decision flowchart.
  The coordinator must write the routing plan to the store BEFORE delegating.
---

# Skill: Dynamic Routing

This skill is always active for the coordinator. It provides structured guidance for classifying any incoming task into one of four types and selecting the minimal effective pipeline.

## The Four Task Types

### 1. Research-Only
**Pipeline:** coordinator → researcher → writer

The user wants to know something, not do something. No recommendation is being sought. No decision needs to be made.

**Signals:**
- Starts with "what is", "what are", "tell me about", "explain", "describe", "summarise"
- No action verb implying the user wants to change something
- No "should I", "how do I", "what should we do"
- Scope is bounded (a topic, a concept, a factual question)

**Examples:** "What is horizontal pod autoscaling?", "Tell me about the history of CRISPR", "Summarise the main arguments for and against remote work"

---

### 2. Analysis
**Pipeline:** coordinator → researcher → strategist → critic → writer

The user has a situation and wants help thinking through it. A recommendation is needed, but it doesn't imply the user will immediately act — they may be deciding whether to act.

**Signals:**
- "what should I do about X", "how should I approach Y", "is X a good idea"
- "evaluate", "assess", "help me think through"
- A problem is described and the user wants structured thinking applied to it
- Stakes are moderate — the decision matters but is not irreversible

**Examples:** "What should I do about our slow CI pipeline?", "Help me evaluate whether to migrate to a microservices architecture", "How should I approach the conversation with my difficult stakeholder?"

---

### 3. Full Pipeline
**Pipeline:** coordinator → researcher → strategist → critic → writer

The user wants a strategy, plan, or recommendation where the stakes are significant. Same pipeline as analysis — the distinction is degree of stakes and formality of output.

**Signals:**
- "strategy", "plan", "roadmap", "framework", "recommendations"
- Output will inform a real decision with meaningful consequences
- Multi-step or multi-party involvement is implied
- The user wants something they can present or act on directly

**Examples:** "Give me a strategy for improving our engineering team's velocity", "What's the best approach for scaling our data infrastructure?", "Create a framework for evaluating vendor proposals"

---

### 4. Action-Required
**Pipeline:** coordinator → researcher → strategist → critic → writer → executor

The user wants something done, not just analysed. The executor will act on the writer's output.

**Signals:**
- Action verbs as primary directive: "do", "implement", "execute", "run", "deploy", "create", "build", "set up"
- The output is instructions or a plan the executor can follow mechanically
- Success is defined by a state change in the world, not a document

**Examples:** "Set up a GitHub Actions workflow for our Go project", "Implement the recommendations from last week's review", "Create the directory structure for the new service"

---

## Decision Flowchart

```
Incoming task
    │
    ▼
Does the task contain a primary action verb
(do, implement, execute, run, deploy, build, create, set up)?
    │
    ├─ YES → action-required
    │         pipeline: coordinator → researcher → strategist → critic → writer → executor
    │
    └─ NO
        │
        ▼
    Does the task ask for a strategy, plan, roadmap,
    or recommendation with significant stakes?
        │
        ├─ YES → full-pipeline
        │         pipeline: coordinator → researcher → strategist → critic → writer
        │
        └─ NO
            │
            ▼
        Does the task ask what the user SHOULD DO
        or how to APPROACH a situation?
            │
            ├─ YES → analysis
            │         pipeline: coordinator → researcher → strategist → critic → writer
            │
            └─ NO → research-only
                      pipeline: coordinator → researcher → writer
```

---

## The Routing Plan

The coordinator MUST write the routing plan to `a-team/{chainID}/task-plan` before delegating to any agent. The plan must contain:

1. **Task summary** — one sentence.
2. **Task type** — one of the four types above.
3. **Agent sequence** — ordered list.
4. **Per-agent brief** — what each agent must produce and what key question they answer.

The coordinator must not deviate from the written plan mid-run. If circumstances change (e.g. a gate rejects the researcher's output), update the plan in the store and note the reason before re-delegating.

---

## Edge Cases

**Ambiguous task:** If the task could be `research-only` or `analysis`, default to `analysis`. The extra pipeline agents add quality at low cost. Do NOT default to `full-pipeline` when `analysis` is sufficient — that adds unnecessary latency.

**Very short tasks:** Single-sentence tasks with no context often default to `research-only`. If in doubt, classify based on the verb structure.

**Compound tasks:** "Research X and then implement Y" → `action-required`. Take the highest-level classification.
