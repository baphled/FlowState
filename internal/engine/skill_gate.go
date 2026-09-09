package engine

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/skill"
)

// SkillGuardCircuitBreakerThreshold is the number of consecutive
// skills-first guard rejections tolerated in a session before the
// guard trips its circuit breaker: instead of rejecting a further
// call, it marks the gate satisfied and lets the call proceed.
const SkillGuardCircuitBreakerThreshold = 3

// alwaysActiveSkillsForGate resolves the always-active skill set the
// deterministic auto-inject path can bake into the session.
//
// Expected: no parameters.
// Returns: the construction-time skills slice when populated, otherwise
// the SkillsResolver output for the current manifest, otherwise nil.
// Side effects: None.
func (e *Engine) alwaysActiveSkillsForGate() []skill.Skill {
	e.mu.RLock()
	loaded := e.skills
	resolver := e.skillsResolver
	e.mu.RUnlock()
	if len(loaded) > 0 {
		return loaded
	}
	if resolver == nil {
		return nil
	}
	return resolver(e.Manifest())
}

// autoInjectAlwaysActiveSkills deterministically activates the session's
// always-active skills by appending their full bodies to the session
// history as a system message, then marking skill_load as completed so
// the harness guard has nothing left to reject.
//
// Expected: sessionID identifies the session the gate is evaluating.
// Returns: true when skill content was found and injected.
// Side effects: appends a system message to the context store and marks
// skill_load completed for the session.
func (e *Engine) autoInjectAlwaysActiveSkills(sessionID string) bool {
	skills := e.alwaysActiveSkillsForGate()
	if len(skills) == 0 {
		return false
	}
	var sb strings.Builder
	sb.WriteString("Your always-active skills have been auto-loaded and are active NOW. Act on their instructions (e.g. any Phase 0 classification a discovery skill prescribes) before further tool calls.\n\n")
	for _, s := range skills {
		sb.WriteString(fmt.Sprintf("# Skill: %s\n%s\n\n", s.Name, s.Content))
	}
	if e.store != nil {
		e.store.Append(provider.Message{Role: "system", Content: sb.String()})
	}
	e.markSkillLoadCalled(sessionID)
	slog.Info("skills-first guard auto-injected always-active skills",
		"session", sessionID,
		"skills", len(skills),
	)
	return true
}

// tripSkillGuardCircuitBreaker handles the exhausted-rejection path: the
// guard has rejected the configured number of consecutive calls without
// the model complying, so it marks the gate satisfied, injects a notice
// into the session history, and lets subsequent calls proceed.
//
// Expected: sessionID identifies the session the gate is evaluating.
// Returns: nothing.
// Side effects: appends a notice system message to the context store and
// marks skill_load completed for the session.
func (e *Engine) tripSkillGuardCircuitBreaker(sessionID string) {
	notice := "The skills-first guard circuit breaker tripped: repeated skill_load-first rejections did not produce a skill_load call. The gate is now satisfied; proceed with your task."
	if e.store != nil {
		e.store.Append(provider.Message{Role: "system", Content: notice})
	}
	e.markSkillLoadCalled(sessionID)
	slog.Warn("skills-first guard circuit breaker tripped — auto-satisfying gate",
		"session", sessionID,
		"threshold", SkillGuardCircuitBreakerThreshold,
	)
}

// recordSkillGuardRejection increments the per-session consecutive
// rejection counter.
//
// Expected: sessionID identifies the session the gate rejected.
// Returns: the updated consecutive rejection count.
// Side effects: mutates the skillGuardRejections map.
func (e *Engine) recordSkillGuardRejection(sessionID string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.skillGuardRejections[sessionID]++
	return e.skillGuardRejections[sessionID]
}

// skillGuardRejectionCount returns the per-session consecutive rejection count.
//
// Expected: sessionID identifies the session.
// Returns: the number of consecutive skills-first rejections recorded.
// Side effects: None.
func (e *Engine) skillGuardRejectionCount(sessionID string) int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.skillGuardRejections[sessionID]
}
