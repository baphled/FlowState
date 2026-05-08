// Package truncate caps tool outputs at a per-tool byte/line budget,
// spilling the original to a session-scoped overflow file and embedding
// a recovery hint into the truncated payload.
//
// The contract mirrors OpenCode's tool/truncation.ts: callers pass the
// raw output to Apply and receive either the unchanged payload (under
// cap) or a truncated payload ending with a hint that points at the
// overflow file plus tells the agent how to recover specific ranges
// using the read tool's offset/limit fields or grep.
//
// Defaults (MaxLines=2000, MaxBytes=50KB) are tuned for shell-style
// tool outputs (bash, read, grep, ls). Tools whose envelope already
// caps output (e.g. web at 10KB) can pass tighter limits via Options.
package truncate
