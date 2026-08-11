// Package atomicwrite provides crash-safe file writes via the
// write-temp-then-rename-with-fsync pattern.
//
// This package handles:
//   - Atomic file persistence to prevent corruption on crash or power loss
//   - Temp-file creation, fsync, and durable rename over the target path
package atomicwrite
