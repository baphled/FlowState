package chat

// AtBottom returns whether the viewport is currently tracking the bottom position.
// This is used for preserving scroll position during streaming.
//
// Returns: bool indicating if viewport is at bottom (true) or scrolled up (false).
//
// Side effects: None (read-only accessor).
func (i *Intent) AtBottom() bool {
	return i.atBottom
}

// ViewportYOffset returns the current message viewport Y offset for testing and introspection.
//
// Returns: int Y offset of viewport, or -1 if viewport is nil.
//
// Side effects: None (read-only accessor).
func (i *Intent) ViewportYOffset() int {
	if i.msgViewport == nil {
		return -1
	}
	return i.msgViewport.YOffset
}
