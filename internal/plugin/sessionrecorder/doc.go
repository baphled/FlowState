// Package sessionrecorder provides a production plugin that captures full session
// timelines (EventBus events and stream chunks) to JSONL files for debugging
// and golden-file test generation.
//
// Responsibilities:
//   - Subscribing to all EventBus event types
//   - Recording stream chunks via the Recorder interface
//   - Writing chronological JSONL timeline entries to per-session files
package sessionrecorder
