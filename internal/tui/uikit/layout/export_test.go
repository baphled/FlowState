package layout

// TokenColor exposes tokenColor for testing.
var TokenColor = tokenColor

// SecondaryContent exposes the private secondaryContent field for testing.
func SecondaryContent(sl *ScreenLayout) string {
	return sl.secondaryContent
}
