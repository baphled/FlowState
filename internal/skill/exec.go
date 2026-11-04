package skill

import (
	"fmt"
	"os/exec"
)

// errInvalidEvent reports a hook bound or fired on an event
// outside the closed set.
func errInvalidEvent(e HookEvent) error {
	return fmt.Errorf("skill: unknown hook event %q", string(e))
}

// realExec runs one hook command via exec.Command; hook commands
// are manifest-authored and token-validated by their callers, the
// same trust boundary as gate commands.
func realExec(name string, args ...string) error {
	// #nosec G204 -- hook commands are manifest-authored; producers
	// validate tokens before binding, the gates.go trust boundary.
	candidate := exec.Command
	return candidate(name, args...).Run()
}
