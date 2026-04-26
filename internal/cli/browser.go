package cli

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenURL launches the user's default browser at url. Picks the right
// command per OS:
//
//   - linux:   xdg-open
//   - darwin:  open
//   - windows: cmd /c start "" <url>  (the empty quoted title slot is
//     required so `start` does not interpret a URL with spaces as the
//     window title; FlowState's URLs are quote-free in practice but the
//     pattern is the de facto idiom).
//
// Failure is non-fatal: callers should print a fallback "open this URL
// manually" message rather than aborting. Returns the underlying error
// for logging when the spawn fails.
//
// Expected:
//   - url is a non-empty, schema-prefixed URL.
//
// Returns:
//   - nil when the browser process was successfully spawned.
//   - An error when the per-OS command is missing or fails to start.
//
// Side effects:
//   - Spawns a detached process (Start, not Run); the browser keeps
//     running after the FlowState process exits.
func OpenURL(url string) error {
	cmd, args := browserCommand(url)
	if cmd == "" {
		return fmt.Errorf("no browser command known for runtime %q", runtime.GOOS)
	}
	return exec.Command(cmd, args...).Start()
}

// browserCommand returns the per-OS executable + args for opening url.
// Split out so tests can pin the command shape per platform without
// actually spawning a browser.
//
// Expected:
//   - url is a non-empty URL.
//
// Returns:
//   - Empty cmd when runtime.GOOS is unrecognised.
//   - The executable name and its argument slice otherwise.
//
// Side effects:
//   - None.
func browserCommand(url string) (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "cmd", []string{"/c", "start", "", url}
	case "linux", "freebsd", "openbsd", "netbsd":
		return "xdg-open", []string{url}
	default:
		return "", nil
	}
}
