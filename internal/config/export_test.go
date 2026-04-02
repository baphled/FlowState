package config

// ExpandTildeForTest exposes expandTilde for testing.
func ExpandTildeForTest(path string) string {
	return expandTilde(path)
}

// ExpandPathsForTest exposes expandPaths for testing.
func ExpandPathsForTest(cfg *AppConfig) {
	expandPaths(cfg)
}
