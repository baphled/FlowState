package swarm

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// ValidateManifest checks a configured swarm manifest for internal
// consistency: a name and scope are required, packet ids are unique and use
// the PacketIDPattern charset, every packet sits within the manifest scope,
// and gate names are unique and non-empty. Every free-text field that reaches
// the coordinator's brief is bounded (see the Max constants) and free of
// control, format and line-separator characters (a task may hold \n and \t),
// and the rendered brief must fit MaxBriefBytes. It returns the first
// violation found.
func ValidateManifest(m Manifest) error {
	if err := validateManifestHead(m); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, p := range m.Packets {
		if err := validatePacketID(p.ID); err != nil {
			return err
		}
		if seen[p.ID] {
			return fmt.Errorf("duplicate packet id %q", p.ID)
		}
		seen[p.ID] = true
		if err := ValidatePacket(p); err != nil {
			return fmt.Errorf("packet %s: %w", p.ID, err)
		}
		if err := validatePacketRole(p); err != nil {
			return err
		}
		if !scopeWithin(p.Scope, m.Scope) {
			return fmt.Errorf("packet %s scope %q escapes manifest scope %q", p.ID, p.Scope, m.Scope)
		}
	}
	if err := validatePrerequisiteGraph(m.Packets); err != nil {
		return err
	}
	if err := validateGates(m.Gates); err != nil {
		return err
	}
	if n := len(Brief(m)); n > MaxBriefBytes {
		return fmt.Errorf("manifest brief is %d bytes, over the %d byte limit", n, MaxBriefBytes)
	}
	return nil
}

// validateManifestHead checks the manifest-level fields and the collection sizes.
func validateManifestHead(m Manifest) error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("manifest name is required")
	}
	if strings.TrimSpace(m.Scope) == "" {
		return fmt.Errorf("manifest scope must not be empty")
	}
	if err := validateText("manifest name", m.Name, MaxNameBytes, false); err != nil {
		return err
	}
	if err := validateText("manifest scope", m.Scope, MaxScopeBytes, false); err != nil {
		return err
	}
	if len(m.Packets) > MaxPackets {
		return fmt.Errorf("manifest has %d packets, over the %d packet limit", len(m.Packets), MaxPackets)
	}
	if len(m.Gates) > MaxGates {
		return fmt.Errorf("manifest has %d gates, over the %d gate limit", len(m.Gates), MaxGates)
	}
	return validateProfileName("coordinator", m.Coordinator)
}

// validateGates checks gate names, descriptions and commands.
func validateGates(gates []Gate) error {
	names := map[string]bool{}
	for _, g := range gates {
		if strings.TrimSpace(g.Name) == "" {
			return fmt.Errorf("gate name is required")
		}
		if err := validateText("gate name", g.Name, MaxGateNameBytes, false); err != nil {
			return err
		}
		if err := validateText("gate description", g.Description, MaxGateDescriptionBytes, false); err != nil {
			return err
		}
		if names[g.Name] {
			return fmt.Errorf("duplicate gate name %q", g.Name)
		}
		names[g.Name] = true
		if strings.TrimSpace(g.Command) == "" {
			return fmt.Errorf("gate %s: command is required", g.Name)
		}
	}
	return nil
}

// validatePacketID refuses an id outside PacketIDPattern; an empty id is left
// to ValidatePacket. The id is quoted in the error: it is operator data.
func validatePacketID(id string) error {
	if id != "" && !packetIDRE.MatchString(id) {
		return fmt.Errorf("packet id %q does not match %s", id, PacketIDPattern)
	}
	return nil
}

// validateText bounds a free-text field to limit bytes and refuses control,
// format (Cf) and line or paragraph separator characters, which could hide or
// forge structure in the brief. multiline additionally allows \n and \t.
func validateText(field, s string, limit int, multiline bool) error {
	if len(s) > limit {
		return fmt.Errorf("%s is %d bytes, over the %d byte limit", field, len(s), limit)
	}
	for _, r := range s {
		if multiline && (r == '\n' || r == '\t') {
			continue
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' {
			return fmt.Errorf("%s holds the disallowed character %U", field, r)
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

// gateStopGrace is how long a cancelled gate's process group gets to exit
// after SIGTERM (Unix) before the group is killed. It is shorter than
// gateWaitDelay: os/exec abandons the wait and kills only the direct process
// when gateWaitDelay expires, so the group kill has to land first.
const gateStopGrace = 3 * time.Second

// NewGateRunner builds a runner with the real shell executor.
func NewGateRunner() *GateRunner {
	return NewGateRunnerWithGrace(gateStopGrace)
}

// NewGateRunnerWithGrace builds a runner with the real shell executor whose
// cancelled gates get grace to exit after SIGTERM before their process group
// is killed (Unix only; elsewhere the gate is killed at once). A grace that is
// not positive or exceeds the default of three seconds is replaced by the
// default, so a cancelled run always returns within gateWaitDelay.
func NewGateRunnerWithGrace(grace time.Duration) *GateRunner {
	return newGateRunnerIn("", grace)
}

// NewGateRunnerIn builds a runner with the real shell executor whose gate
// commands run in dir (cmd.Dir; the environment is unchanged). Gates judge the
// tree the work was done in, so an executed swarm needs a runner whose Dir is
// its workdir.
func NewGateRunnerIn(dir string) *GateRunner {
	return newGateRunnerIn(dir, gateStopGrace)
}

// newGateRunnerIn is the real executor in dir with the given stop grace.
func newGateRunnerIn(dir string, grace time.Duration) *GateRunner {
	if grace <= 0 || grace > gateStopGrace {
		grace = gateStopGrace
	}
	return &GateRunner{dir: dir, execCommand: func(ctx context.Context, name string, args []string) error {
		return runGate(ctx, dir, name, args, grace)
	}}
}

// NewGateRunnerWithIn builds a runner around a caller-supplied executor that
// declares the directory it runs its commands in (see Dir).
func NewGateRunnerWithIn(dir string, executor GateExecutor) *GateRunner {
	return &GateRunner{dir: dir, execCommand: executor}
}

// Dir is the directory the runner's commands run in; empty means the process
// working directory (and, for NewGateRunnerWith, that nothing is declared).
func (r *GateRunner) Dir() string { return r.dir }

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
// isolateGate): SIGTERM first, SIGKILL after grace; elsewhere only
// the direct process is killed. The gate's output is not piped (os/exec
// connects a nil Stdout to the null device), so a surviving child
// cannot hold Run open; WaitDelay bounds the wait regardless.
func runGate(ctx context.Context, dir, name string, args []string, grace time.Duration) error {
	if err := validateCommandTokens(append([]string{name}, args...)); err != nil {
		return err
	}
	candidate := exec.CommandContext
	cmd := candidate(ctx, name, args...)
	cmd.Dir = dir
	cmd.WaitDelay = gateWaitDelay
	settle := isolateGate(cmd, grace)
	defer settle()
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

// Bounds on a manifest. Every limit is in bytes except the counts. The packet
// id limit (64) is part of PacketIDPattern. MaxBriefBytes caps the whole
// rendered brief, which the per-field limits alone would not (64 packets of
// MaxPacketTaskBytes is 512 KiB).
const (
	// MaxPacketTaskBytes bounds a packet's task text: it becomes part of the
	// coordinator's brief, so an unbounded task would let a manifest swamp it.
	MaxPacketTaskBytes = 8192
	// MaxPackets is the most packets a manifest may hold.
	MaxPackets = 64
	// MaxGates is the most gates a manifest may hold.
	MaxGates = 16
	// MaxPrerequisites is the most prerequisites one packet may name.
	MaxPrerequisites = 16
	// MaxNameBytes bounds the swarm name.
	MaxNameBytes = 100
	// MaxScopeBytes bounds the manifest scope.
	MaxScopeBytes = 200
	// MaxGateNameBytes bounds a gate name.
	MaxGateNameBytes = 100
	// MaxGateDescriptionBytes bounds a gate description.
	MaxGateDescriptionBytes = 500
	// MaxBriefBytes bounds the rendered coordinator brief.
	MaxBriefBytes = 65536
)

// PacketIDPattern is the charset of a packet id in a manifest. It excludes
// whitespace, brackets and quotes, so every accepted id round-trips through
// the `[packet <id>]` marker (see PacketRef).
const PacketIDPattern = `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`

// packetIDRE compiles PacketIDPattern.
var packetIDRE = regexp.MustCompile(PacketIDPattern)

// profileNameRE is the shape of an agent profile name (internal/agent's rule).
var profileNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// validateProfileName accepts an empty name (the field is optional) or a
// well-formed profile name; field names the manifest key in the error.
func validateProfileName(field, name string) error {
	if name != "" && !profileNameRE.MatchString(name) {
		return fmt.Errorf("%s %q is not a profile name (lower-case letters, digits and dashes)", field, name)
	}
	return nil
}

// validatePacketRole checks the packet's optional role and bounds its task
// and prerequisite count. A task of only whitespace is refused.
func validatePacketRole(p Packet) error {
	if strings.TrimSpace(p.Task) == "" {
		return fmt.Errorf("packet %s: task must not be blank", p.ID)
	}
	if err := validateText("task", p.Task, MaxPacketTaskBytes, true); err != nil {
		return fmt.Errorf("packet %s: %w", p.ID, err)
	}
	if len(p.Prerequisites) > MaxPrerequisites {
		return fmt.Errorf("packet %s: %d prerequisites, over the %d limit", p.ID, len(p.Prerequisites), MaxPrerequisites)
	}
	if err := validateProfileName("role", p.Role); err != nil {
		return fmt.Errorf("packet %s: %w", p.ID, err)
	}
	return nil
}
