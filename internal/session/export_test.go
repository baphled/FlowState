package session

// ExtractPrimaryArgForTest exposes extractPrimaryArg for external test assertions.
func ExtractPrimaryArgForTest(name string, args map[string]any) string {
	return extractPrimaryArg(name, args)
}
