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

// matchesPattern is the single shared glob predicate. A pattern with a
// trailing `*` matches any model whose name starts with the prefix
// before the `*`. A pattern without `*` is a literal exact match.
//
// Examples:
//   - `claude-*` matches `claude-sonnet-4-20250514`, `claude-3-5-haiku`.
//   - `qwen3:*` matches `qwen3:8b`, `qwen3:14b`, `qwen3:30b-a3b`.
//   - `glm-4.7` matches only `glm-4.7` (no glob suffix → literal).
//
// Empty pattern never matches; empty model never matches.
func matchesPattern(model, pattern string) bool {
	if model == "" || pattern == "" {
		return false
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(model, prefix)
	}
	return model == pattern
}
