package ollama

// ResolveOllamaContextLengthForTest is a test-only export of the
// unexported resolveOllamaContextLength so external_test specs can drive
// it without widening the production API surface.
func ResolveOllamaContextLengthForTest(modelID string) int {
	return resolveOllamaContextLength(modelID)
}
