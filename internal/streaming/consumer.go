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
