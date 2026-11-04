// Package skill owns generic skill loading (SKILL.md-style
// markdown) and event-conditioned hook execution for any
// manifest-driven loop.
package skill

// Skill is one loaded skill: a name, a short description and the
// guidance body. Skills are markdown files (SKILL.md) with a
// YAML frontmatter block; nothing executes at load time.
type Skill struct {
	// Name identifies the skill (frontmatter name).
	Name string
	// Description is the one-line frontmatter description.
	Description string
	// Body is the markdown guidance after the frontmatter.
	Body string
}

// HookEvent is the closed set of run events hooks may bind to.
type HookEvent string

// The generic run events a hook may condition on; producers of
// these events are any loop that reports RunEvents-shaped progress.
const (
	// EventPacketAdmitted fires after one packet is admitted.
	EventPacketAdmitted HookEvent = "packet-admitted"
	// EventPacketRejected fires after one packet is refused.
	EventPacketRejected HookEvent = "packet-rejected"
	// EventGateFailed fires after one gate does not pass.
	EventGateFailed HookEvent = "gate-failed"
	// EventGatePassed fires after one gate passes.
	EventGatePassed HookEvent = "gate-passed"
)

// Hook is one event-conditioned hook: when the named event fires
// with a matching subject, the command runs.
type Hook struct {
	// Event is the bound event name.
	Event HookEvent
	// Subject narrows the hook to one packet or gate id; empty
	// binds to every subject.
	Subject string
	// Name is the command word to execute.
	Name string
	// Args are the command arguments.
	Args []string
}

// Runner executes hooks against reported events.
type Runner struct {
	// hooks are the registered bindings.
	hooks []Hook
	// exec runs one command; mirrors exec.Command for tests.
	exec func(name string, args ...string) error
	// fired records the hooks that ran, for tests/inspection.
	fired []Hook
}
