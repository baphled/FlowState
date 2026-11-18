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
	if err := validatePrerequisiteGraph(m.Packets); err != nil {
		return err
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

// validatePrerequisiteGraph rejects unknown, repeated and cyclic dependencies
// before RunOrder can silently omit work from the execution sequence.
func validatePrerequisiteGraph(packets []Packet) error {
	degree := map[string]int{}
	dependents := map[string][]string{}
	for _, p := range packets {
		degree[p.ID] = 0
	}
	for _, p := range packets {
		seen := map[string]bool{}
		for _, prerequisite := range p.Prerequisites {
			if _, exists := degree[prerequisite]; !exists {
				return fmt.Errorf("packet %s: unknown prerequisite %q", p.ID, prerequisite)
			}
			if seen[prerequisite] {
				return fmt.Errorf("packet %s: duplicate prerequisite %q", p.ID, prerequisite)
			}
			seen[prerequisite] = true
			degree[p.ID]++
			dependents[prerequisite] = append(dependents[prerequisite], p.ID)
		}
	}
	var ready []string
	for _, p := range packets {
		if degree[p.ID] == 0 {
			ready = append(ready, p.ID)
		}
	}
	processed := 0
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		processed++
		for _, child := range dependents[id] {
			degree[child]--
			if degree[child] == 0 {
				ready = append(ready, child)
			}
		}
	}
	if processed != len(packets) {
		return fmt.Errorf("manifest prerequisite cycle detected")
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
