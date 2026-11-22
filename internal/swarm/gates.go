package swarm

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
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

// gateWaitDelay bounds how long Run waits after the gate's process was
// killed for its I/O to drain.
const gateWaitDelay = 5 * time.Second

// NewGateRunner builds a runner with the real shell executor.
func NewGateRunner() *GateRunner {
	return &GateRunner{execCommand: runGate}
}

// NewGateRunnerWith builds a runner around a caller-supplied executor,
// the dependency-injection seam for tests and alternative sandboxes.
func NewGateRunnerWith(executor GateExecutor) *GateRunner {
	return &GateRunner{execCommand: executor}
}

// Evaluate runs every gate and aggregates the decision. A manifest
// with no gates fails closed: acceptance requires at least one gate.
// Cancelling ctx is honoured between gates (the next gate is not
// started) and during one (the executor stops its command); a gate
// that never started has no outcome and the decision is not accepted.
// A gate whose command returns an error while ctx is done is recorded
// as failed and Interrupted; a cancellation that lands between the
// check below and the launch is indistinguishable from one during the
// run, so that gate is recorded the same way. Decision.Interrupted
// says whether cancellation cut the evaluation short.
func (r *GateRunner) Evaluate(ctx context.Context, m Manifest) GatesDecision {
	decision := GatesDecision{}
	for _, g := range m.Gates {
		if ctx.Err() != nil {
			decision.Interrupted = true
			return decision
		}
		parts := strings.Fields(g.Command)
		out := GateOutcome{Gate: g, Passed: false}
		if len(parts) > 0 {
			err := r.execCommand(ctx, parts[0], parts[1:])
			out.Passed = err == nil
			out.Interrupted = err != nil && ctx.Err() != nil
			decision.Interrupted = decision.Interrupted || out.Interrupted
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
//
// Cancelling ctx stops the gate's whole process group on Unix (see
// isolateGate), so its children do not outlive the run; elsewhere only
// the direct process is killed. The gate's output is not piped (os/exec
// connects a nil Stdout to the null device), so a surviving child
// cannot hold Run open; WaitDelay bounds the wait regardless.
func runGate(ctx context.Context, name string, args []string) error {
	if err := validateCommandTokens(append([]string{name}, args...)); err != nil {
		return err
	}
	candidate := exec.CommandContext
	cmd := candidate(ctx, name, args...)
	cmd.WaitDelay = gateWaitDelay
	isolateGate(cmd)
	return cmd.Run()
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
