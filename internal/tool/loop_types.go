package tool

import "context"

// EventSink appends ordered session events; implemented by the
// store's single-writer EventWriter, kept abstract so the tool
// domain does not import the store adapter directly.
type EventSink interface {
	// Append records one event; implementors serialize per session.
	Append(ctx context.Context, actor, typ string, payload any) (int64, error)
}

// Loop executes tool invocations for one session against a registry,
// enforcing the per-session allowlist and recording ordered
// tool.call / tool.result events through the session's EventWriter.
type Loop struct {
	registry  *Registry
	writer    EventSink
	allowlist map[string]bool
}
