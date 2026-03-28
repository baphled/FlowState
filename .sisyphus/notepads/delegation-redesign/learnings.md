# Learnings — delegation-redesign

<!-- Append entries using format: ## [TIMESTAMP] Task: {task-id} -->
## [2026-03-28] Task: T1 — Aliases field
- Manifest struct location: internal/agent/manifest.go:4
- Existing test pattern: Ginkgo Describe/Context/It with json.Unmarshal assertions in internal/agent/manifest_test.go
- JSON tag pattern used by existing fields: snake_case without omitempty for required manifest properties
- Gotcha: missing aliases stays nil with default encoding/json, so Manifest now defines UnmarshalJSON to normalise it to an empty slice
