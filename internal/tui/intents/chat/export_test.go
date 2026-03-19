package chat

// SetStreamingForTest sets the streaming state for testing purposes.
func (i *Intent) SetStreamingForTest(streaming bool) {
	i.streaming = streaming
}
