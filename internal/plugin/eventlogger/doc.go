// Package eventlogger provides a concurrent-safe JSONL event logger that
// subscribes to an EventBus and writes structured event records to a file.
//
// This package handles:
//   - Subscribing to all event types on a plugin EventBus
//   - Writing each event as a single JSONL line with type, timestamp, and data
//   - Size-based file rotation when the log exceeds a configured maximum
//   - Mutex-guarded file operations for concurrent safety
package eventlogger
