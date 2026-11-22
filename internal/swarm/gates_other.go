//go:build !unix

package swarm

import "os/exec"

// isolateGate (Windows and other non-Unix platforms) keeps the default behaviour on platforms without Unix
// process groups: cancellation kills only the gate's direct process.
// Process groups (job objects on Windows) are not handled here, so a
// gate's children can outlive a cancelled run.
func isolateGate(*exec.Cmd) {}
