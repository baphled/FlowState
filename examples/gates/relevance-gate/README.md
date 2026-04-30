# Relevance Gate

A post-member gate that validates the researcher's output is substantively on-topic for the original task before passing to downstream agents.

## Algorithm

1. Extract keywords from `task_plan` and `research` (words 4+ characters, stopwords removed)
2. Compute overlap score: `|task_keywords ∩ research_keywords| / |task_keywords|`
3. Compare against `policy.threshold` (default: 0.4)
4. Return `pass: true` with score and matched keywords, or `pass: false` with missing topics and a redirect signal

## Input

```json
{
  "kind": "relevance-gate",
  "payload": {
    "task_plan": "the coordinator's routing plan describing the task",
    "research": "the researcher's output from the coordination store"
  },
  "policy": {
    "threshold": 0.4
  }
}
```

## Output

Pass:
```json
{"pass": true, "score": 0.72, "overlap": ["deployment", "kubernetes", "scaling"]}
```

Fail:
```json
{
  "pass": false,
  "reason": "research relevance score 0.12 below threshold 0.40",
  "missing_topics": ["autoscaling", "deployment", "kubernetes", "metrics", "replicas"],
  "redirect": "Research should cover: autoscaling, deployment, kubernetes, metrics, replicas"
}
```

## Testing

```bash
# Empty research → fail
echo '{"kind":"relevance-gate","payload":{"task_plan":"machine learning model deployment pipeline","research":""}}' \
  | python3 gate.py

# Irrelevant research → fail with missing_topics
echo '{"kind":"relevance-gate","payload":{"task_plan":"kubernetes deployment autoscaling","research":"The history of ancient Rome and the Roman Empire began in 753 BCE."}}' \
  | python3 gate.py

# Relevant research → pass with score
echo '{"kind":"relevance-gate","payload":{"task_plan":"kubernetes deployment autoscaling","research":"Kubernetes horizontal pod autoscaling scales deployment replicas based on CPU metrics."}}' \
  | python3 gate.py
```
