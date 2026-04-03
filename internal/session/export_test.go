package session

// ExtractPrimaryArgForTest exposes the shared tool display logic for external test assertions.
func ExtractPrimaryArgForTest(name string, args map[string]any) string {
	return primaryArgValue(name, args)
}
