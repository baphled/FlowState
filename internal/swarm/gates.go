package swarm

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// ValidateManifest checks a configured swarm manifest for internal: a name and scope are required, packet ids are unique,
// consistency: a name and scope are required, packet ids are unique,
// every packet sits within the manifest scope, and gate names are
// unique and non-empty. It returns the first violation found.
func ValidateManifest(m Manifest) error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("manifest name is required")
	}
	if strings.TrimSpace(m.Scope) == "" {
		return fmt.Errorf("manifest scope must not be empty")
	}
	seen := map[string]bool{}
	for _, p := range m.Packets {
		if seen[p.ID] {
			return fmt.Errorf("duplicate packet id %q", p.ID)
		}
		seen[p.ID] = true
		if err := ValidatePacket(p); err != nil {
			return fmt.Errorf("packet %s: %w", p.ID, err)
		}
		if !scopeWithin(p.Scope, m.Scope) {
			return fmt.Errorf("packet %s scope %q escapes manifest scope %q", p.ID, p.Scope, m.Scope)
		}
	}
	gates := map[string]bool{}
	for _, g := range m.Gates {
		if strings.TrimSpace(g.Name) == "" {
			return fmt.Errorf("gate name is required")
		}
		if gates[g.Name] {
			return fmt.Errorf("duplicate gate name %q", g.Name)
		}
		gates[g.Name] = true
		if strings.TrimSpace(g.Command) == "" {
			return fmt.Errorf("gate %s: command is required", g.Name)
		}
	}
	return nil
}

// NewGateRunner builds a runner with the real shell executor.
func NewGateRunner() *GateRunner {
	return &GateRunner{execCommand: func(name string, args ...string) gateOutput {
		return gateOutput{err: runGate(name, args)}
	}}
}

// Evaluate runs every gate and aggregates the decision. A manifest
// with no gates fails closed: acceptance requires at least one gate.
func (r *GateRunner) Evaluate(m Manifest) GatesDecision {
	decision := GatesDecision{}
	for _, g := range m.Gates {
		parts := strings.Fields(g.Command)
		out := GateOutcome{Gate: g, Passed: false}
		if len(parts) > 0 {
			out.Passed = r.execCommand(parts[0], parts[1:]...).err == nil
		}
		decision.Outcomes = append(decision.Outcomes, out)
	}
	decision.Accepted = len(decision.Outcomes) > 0
	for _, o := range decision.Outcomes {
		if !o.Passed {
			decision.Accepted = false
			break
		}
	}
	return decision
}

// FailedGateNames lists the names of gates that did not pass, sorted.
func (d GatesDecision) FailedGateNames() []string {
	var failed []string
	for _, o := range d.Outcomes {
		if !o.Passed {
			failed = append(failed, o.Gate.Name)
		}
	}
	sort.Strings(failed)
	return failed
}

// runGate launches one operator-authored gate command. The command
// text is validated against a conservative token policy before
// launch: every token must be a recognised command word, flag,
// path-like operand or quoted literal.
func runGate(name string, args []string) error {
	if err := validateCommandTokens(append([]string{name}, args...)); err != nil {
		return err
	}
	candidate := exec.Command
	return candidate(name, args...).Run()
}

// validateCommandTokens rejects tokens outside the conservative gate
// grammar; anything else is a manifest authoring error, not a shell.
func validateCommandTokens(tokens []string) error {
	for _, t := range tokens {
		if t == "" {
			return fmt.Errorf("empty command token")
		}
		if strings.ContainsAny(t, ";|&$`><") || strings.ContainsAny(t, "\n\r") {
			return fmt.Errorf("token %q uses shell metacharacters", t)
		}
	}
	return nil
}
