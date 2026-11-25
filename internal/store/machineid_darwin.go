//go:build darwin

package store

import (
	"context"
	"os/exec"
	"regexp"
	"time"
)

// ioregTimeout bounds the ioreg subprocess.
const ioregTimeout = 5 * time.Second

// platformUUIDLine matches the ioreg line `"IOPlatformUUID" = "<UUID>"`.
var platformUUIDLine = regexp.MustCompile(`"IOPlatformUUID"\s*=\s*"([0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12})"`)

// runIoreg returns the output of ioreg for the platform expert device; tests
// replace it. The command path and arguments are constants (no PATH lookup, no
// caller input), so gosec G204 does not apply.
var runIoreg = func() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ioregTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "/usr/sbin/ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
}

// readMachineID is the per-OS read seam. The per-machine hardware UUID
// (IOPlatformUUID) is parsed from ioreg output, as common machine-id libraries
// do. kern.uuid is NOT used: it identifies the kernel image, not the machine.
// Recalled, unverified on a real macOS machine. Any failure returns "" so the
// host fallback applies.
var readMachineID = func() string {
	out, err := runIoreg()
	if err != nil {
		return ""
	}
	m := platformUUIDLine.FindSubmatch(out)
	if m == nil {
		return ""
	}
	return string(m[1])
}
