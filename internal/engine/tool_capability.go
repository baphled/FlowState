package engine

import "strings"

// IsToolCapableModel reports whether the given (provider, model) pair is
// safe to delegate to: it must match at least one allow-list pattern AND
// must NOT match any deny-list pattern. The deny list takes precedence
// over the allow list — an operator who keeps the default deny list and
// also lists `qwen2.5-coder:14b` under `tool_capable_models` still gets
// the model rejected, because the KB evidence of broken tool-calls is a
// fail-closed signal.
//
// Unknown models (those that match neither list) are treated as NOT
// capable. Callers fail closed: the user can opt-in by adding a pattern
// to cfg.ToolCapableModels when they want to test a new model. This
// matches the prevention-first stance documented in the GLM Delegation
// Failure investigation: silent zero-tool-call delegations are far more
// expensive to debug than a loud refusal.
//
// The provider argument is part of the signature — even when unused
// today — so future scoping like "anthropic claude-* is fine but
// ollama claude-3-haiku-clone isn't" can land without a callsite churn.
func IsToolCapableModel(_ string, model string, allow, deny []string) bool {
	if model == "" {
		return false
	}
	if matchesAnyPattern(model, deny) {
		return false
	}
	return matchesAnyPattern(model, allow)
}

// matchesAnyPattern reports whether model matches any of the given
// patterns. An empty or nil pattern slice yields false (fail closed).
func matchesAnyPattern(model string, patterns []string) bool {
	for _, pat := range patterns {
		if matchesPattern(model, pat) {
			return true
		}
	}
	return false
}

// matchesPattern is the single shared glob predicate. A pattern may
// contain a single `*` wildcard. Supported shapes:
//   - `prefix*`        — match models whose name starts with prefix.
//   - `*suffix`        — match models whose name ends with suffix.
//   - `prefix*suffix`  — match models matching both ends.
//   - no `*`           — literal exact match.
//
// Examples:
//   - `claude-*` matches `claude-sonnet-4`, `claude-3-5-haiku`.
//   - `qwen3:*` matches `qwen3:8b`, `qwen3:14b`, `qwen3:30b-a3b`.
//   - `gpt-*-mini` matches `gpt-4o-mini`, `gpt-5-mini`, `gpt-5.4-mini`.
//   - `claude-haiku*` matches `claude-haiku-4.5`, `claude-haiku-3-5`.
//   - `glm-4.7` matches only `glm-4.7`.
//
// Multiple `*` characters are interpreted as prefix-up-to-first-star
// plus suffix-after-last-star; middle `*` are not treated as
// independent wildcards. Empty pattern never matches; empty model
// never matches.
func matchesPattern(model, pattern string) bool {
	if model == "" || pattern == "" {
		return false
	}
	firstStar := strings.Index(pattern, "*")
	if firstStar == -1 {
		return model == pattern
	}
	prefix := pattern[:firstStar]
	suffix := pattern[strings.LastIndex(pattern, "*")+1:]
	if len(prefix)+len(suffix) > len(model) {
		return false
	}
	return strings.HasPrefix(model, prefix) && strings.HasSuffix(model, suffix)
}
