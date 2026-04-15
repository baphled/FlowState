// Package sessionid centralises the path-safety contract for session
// identifiers so every layer that builds a filesystem path from a
// sessionID validates by the same ruleset.
//
// Motivation (H4 audit). SessionIDs are user-controllable via the
// --session CLI flag and flow through filepath.Join calls in both
// internal/recall/session_memory.go and internal/context/micro_compaction.go
// to produce on-disk paths of the form
// ${storageDir}/${sessionID}/${artefact}. A malicious or accidental
// input such as "../../tmp/evil" escapes the configured storage root;
// an empty or absolute ID collapses two unrelated sessions into the
// same directory or writes outside the expected tree altogether.
//
// This package solves the problem at the gate, not at the callsite:
// Validate is invoked at the earliest user-facing entry (run.go,
// chat.go, session resolution in the app wiring) and again defensively
// at the storage write sites themselves. Belt-and-braces: layered
// defence is worth the redundant cost because trust boundaries should
// never assume their upstream has already filtered input.
package sessionid

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrInvalidSessionID is the sentinel returned for every rejection so
// callers can errors.Is without matching on a specific sub-reason.
// Messages wrapped around it name the offending input so operators can
// correlate the refusal with the supplied --session value.
var ErrInvalidSessionID = errors.New("sessionid: invalid session identifier")

// Validate returns nil when id is safe to use as a filesystem path
// component and ErrInvalidSessionID (wrapped with a human-readable
// reason) otherwise.
//
// Rejection rules (belt-and-braces; several rules overlap):
//
//   - Empty after strings.TrimSpace. A blank id would collapse into
//     the parent storage directory and cross-contaminate sessions.
//   - Contains "/" or "\\". Either separator lets an attacker build
//     a multi-component path and escape the intended directory.
//   - Starts with a single "." (including "." and ".."). Dot prefixes
//     hide entries on Unix tooling and are the lead character of
//     path-traversal sequences.
//   - Is an absolute path per filepath.IsAbs. Catches "/tmp/evil",
//     "C:\\evil", and any OS-native absolute form.
//   - Contains ".." as a path component after Clean. filepath.Clean
//     normalises "a/../b" to "b" but its presence in the *input* is
//     still a strong signal of attempted escape — and on Windows
//     filepath.IsAbs does not catch UNC or drive-relative escapes
//     that lean on "..". Explicitly refuse.
//   - Contains a NUL byte. Some filesystems truncate at NUL and paths
//     that look different can collide on disk.
//
// Expected:
//   - id is the user-supplied session identifier.
//
// Returns:
//   - nil when every rule passes.
//   - A wrapped ErrInvalidSessionID naming the first failing rule.
//
// Side effects:
//   - None. Validate is a pure function.
func Validate(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: must not be empty", ErrInvalidSessionID)
	}
	if strings.ContainsRune(id, '\x00') {
		return fmt.Errorf("%w: must not contain NUL byte", ErrInvalidSessionID)
	}
	if strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("%w: must not contain path separators (%q)", ErrInvalidSessionID, id)
	}
	if strings.HasPrefix(id, ".") {
		return fmt.Errorf("%w: must not start with %q (%q)", ErrInvalidSessionID, ".", id)
	}
	if filepath.IsAbs(id) {
		return fmt.Errorf("%w: must not be an absolute path (%q)", ErrInvalidSessionID, id)
	}
	// Defence in depth: the separator check above already rejects any
	// compound path, but a future rule relaxation (e.g. nested session
	// paths) would make "a/../b" possible. Reject ".." as a component
	// explicitly so the contract stays stable under future edits.
	for _, part := range strings.FieldsFunc(id, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return fmt.Errorf("%w: must not contain %q as a path component (%q)", ErrInvalidSessionID, "..", id)
		}
	}
	return nil
}
