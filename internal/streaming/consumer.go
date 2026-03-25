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

// HarnessEventConsumer is an optional interface for consumers that support harness event visibility.
// Consumers may implement this interface to receive notifications when the harness retries plan validation.
type HarnessEventConsumer interface {
	// WriteHarnessRetry notifies the consumer that plan validation failed and a retry is starting.
	WriteHarnessRetry(content string)
}
