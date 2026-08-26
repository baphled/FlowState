package streaming

// StreamConsumer is the consumer strategy interface for processing streamed chunks.
type StreamConsumer interface {
	// WriteChunk delivers a content fragment to the consumer.
	WriteChunk(content string) error
	// WriteError reports a streaming error to the consumer.
	WriteError(err error)
	// Done signals that the stream has completed.
	Done()
}

// ToolCallConsumer is an optional interface for consumers that support tool call visibility.
// Consumers may implement this interface to receive notifications when a tool call is invoked.
type ToolCallConsumer interface {
	// WriteToolCall notifies the consumer of a tool invocation by name.
	WriteToolCall(name string)
}

// ToolResultConsumer is an optional interface for consumers that support tool result visibility.
// Consumers may implement this interface to receive notifications when a tool result is available.
type ToolResultConsumer interface {
	// WriteToolResult notifies the consumer of a tool result.
	WriteToolResult(content string)
}

// ToolErrorConsumer is an optional sibling interface for consumers that distinguish
// failed tool executions from successful ones. The streaming runner routes
// chunks whose provider.ToolResultInfo.IsError is true through this channel
// when the consumer implements it, falling back to WriteToolResult so legacy
// consumers (without an error channel) continue to see the content unchanged.
//
// The /api/chat SSE consumer (internal/api/sse_consumer.go) writes a typed
// `tool_error` event distinct from `tool_result`; the frontend's
// handleToolErrorEvent (web/src/stores/chatStore.ts) flips the matching
// running tool_result row to status='error' in-stream, so live tool failures
// (rejected calls, real tool errors) surface as a dedicated error bubble
// rather than as a normal tool_result.
//
// Wire-loss bug (May 2026): pre-fix, deliverToolResult dropped IsError on
// the ephemeral /api/chat path because there was no second channel —
// IsError set the persisted role on the session-scoped turn-poll path
// (via accumulator.go) but the SSE wire on /api/chat was IsError-blind.
// Adding this interface gives the wire side a discriminator.
type ToolErrorConsumer interface {
	// WriteToolError notifies the consumer that a tool execution failed.
	// content is the error text as the engine stamped it on the chunk
	// (typically prefixed with "Error: " when the tool used the
	// Result{Error:err} shape, or the rich Output text when the tool
	// populated it for human-readable failure messages).
	WriteToolError(content string)
}

// DelegationConsumer is an optional interface that StreamConsumer implementations
// may satisfy to receive delegation status updates.
type DelegationConsumer interface {
	// WriteDelegation delivers a delegation status event to the consumer.
	WriteDelegation(event DelegationEvent) error
}

// DelegationProgressConsumer is an optional interface for consumers that support progress updates.
type DelegationProgressConsumer interface {
	// WriteProgress delivers a delegation progress event to the consumer.
	WriteProgress(event ProgressEvent) error
}

// NotificationConsumer is an optional interface for consumers that support completion notifications.
type NotificationConsumer interface {
	// WriteNotification delivers a completion notification event to the consumer.
	WriteNotification(event CompletionNotificationEvent) error
}

// HarnessEventConsumer is an optional interface for consumers that support harness event visibility.
// Consumers may implement this interface to receive notifications about harness lifecycle events.
type HarnessEventConsumer interface {
	// WriteHarnessRetry notifies the consumer that plan validation failed and a retry is starting.
	WriteHarnessRetry(content string)
	// WriteAttemptStart notifies the consumer that the harness is beginning a new attempt.
	WriteAttemptStart(content string)
	// WriteComplete notifies the consumer that the harness has finished evaluation.
	WriteComplete(content string)
	// WriteCriticFeedback notifies the consumer that the harness LLM critic has provided feedback.
	WriteCriticFeedback(content string)
}
