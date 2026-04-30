# A-Team Swarm

A versatile generalist swarm for tasks that don't fit a fixed workflow.

## When to use the A-Team

Use the A-Team when:
- The task is exploratory or cross-domain and doesn't fit a more specialised swarm
- You need research, strategy, and critical review in one run
- You want dynamic routing — not every task needs the full pipeline

Use a more specialised swarm (e.g. `engineering`) when:
- The task has a fixed, well-understood workflow (e.g. "implement and test this feature")
- You need domain-specific tooling or built-in agents not in the A-Team's allowlist
- You want tighter gate coverage at multiple pipeline stages

The A-Team trades specialisation for breadth. It is the right default for ambiguous or novel tasks.

## How the coordinator decides routing

The coordinator applies the `dynamic-routing` skill to classify the task into one of four types:

| Task type | Pipeline | Triggered by |
|---|---|---|
| `research-only` | coordinator → researcher → writer | "what is X", "tell me about Y" |
| `analysis` | coordinator → researcher → strategist → critic → writer | "what should I do about X" |
| `full-pipeline` | coordinator → researcher → strategist → critic → writer | strategy, plan, roadmap requests |
| `action-required` | coordinator → researcher → strategist → critic → writer → executor | "do X", "implement Y" |

The coordinator writes the routing plan to `a-team/{chainID}/task-plan` in the coordination store **before** delegating to any agent. It will not deviate from that plan mid-run without writing an update first.

## What the critic is supposed to do

The critic is an adversarial reviewer. Its job is to find the weakest assumptions in the strategist's output before the writer finalises it. The critic MUST:

- Challenge at least one core assumption (not formatting or style)
- Rate each objection by conviction (1-5) and classify it as `breaks-strategy`, `material-risk`, or `worth-noting`
- Produce at least one objection rated `breaks-strategy` or `material-risk`

**Signs the critic did not engage:**
- Every objection is rated `worth-noting`
- The critique opens with "overall this looks good"
- Objections are vague ("this could be wrong") rather than specific ("this assumes X, which is contradicted by finding Y")
- The critique flags risks the strategist already named — finding nothing new

The writer may disagree with the critic but must explain why with evidence. Ignoring the critic without a rebuttal is not acceptable.

## How to install

```bash
cp -r examples/swarms/a-team/* ~/.config/flowstate/
cp -r examples/gates/relevance-gate ~/.config/flowstate/gates/
flowstate swarm validate a-team
```

Then run with:

```bash
flowstate run a-team "your task here"
```

## Validation commands

```bash
# Validate swarm
flowstate swarm validate a-team

# Test gate: empty research (fail)
echo '{"kind":"relevance-gate","payload":{"task_plan":"machine learning model deployment pipeline","research":""}}' \
  | ~/.config/flowstate/gates/relevance-gate/gate.py

# Test gate: irrelevant research (fail)
echo '{"kind":"relevance-gate","payload":{"task_plan":"kubernetes deployment autoscaling","research":"The history of ancient Rome and the Roman Empire began in 753 BCE."}}' \
  | ~/.config/flowstate/gates/relevance-gate/gate.py

# Test gate: relevant research (pass)
echo '{"kind":"relevance-gate","payload":{"task_plan":"kubernetes deployment autoscaling","research":"Kubernetes horizontal pod autoscaling scales deployment replicas based on CPU metrics."}}' \
  | ~/.config/flowstate/gates/relevance-gate/gate.py
```
