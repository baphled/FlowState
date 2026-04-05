package engine

// CategoryConfig defines model routing defaults for a workload category.
type CategoryConfig struct {
	Model       string  `json:"model" yaml:"model"`
	Provider    string  `json:"provider" yaml:"provider"`
	Temperature float64 `json:"temperature" yaml:"temperature"`
	MaxTokens   int     `json:"max_tokens" yaml:"max_tokens"`
}

// DefaultCategoryRouting returns the built-in category routing map.
//
// Returns:
//   - The default category routing configuration keyed by category name.
//
// Side effects:
//   - None.
func DefaultCategoryRouting() map[string]CategoryConfig {
	return map[string]CategoryConfig{
		"quick": {
			Model:       "fast",
			Temperature: 0.2,
			MaxTokens:   1024,
		},
		"deep": {
			Model:       "reasoning",
			Temperature: 0.7,
			MaxTokens:   4096,
		},
		"visual-engineering": {
			Model:       "vision",
			Temperature: 0.3,
			MaxTokens:   2048,
		},
		"ultrabrain": {
			Model:       "reasoning",
			Temperature: 0.9,
			MaxTokens:   8192,
		},
		"unspecified-low": {
			Model:       "fast",
			Temperature: 0.1,
			MaxTokens:   1024,
		},
		"unspecified-high": {
			Model:       "reasoning",
			Temperature: 0.8,
			MaxTokens:   4096,
		},
		"medium": {
			Model:       "balanced",
			Temperature: 0.5,
			MaxTokens:   2048,
		},
		"low": {
			Model:       "fast",
			Temperature: 0.2,
			MaxTokens:   2048,
		},
	}
}
