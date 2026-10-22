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
