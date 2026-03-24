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
