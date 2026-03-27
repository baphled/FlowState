package engine

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCategoryResolver_Resolve(t *testing.T) {
	t.Run("returns default config for known category", func(t *testing.T) {
		resolver := NewCategoryResolver(nil)
		cfg, err := resolver.Resolve("quick")
		require.NoError(t, err)
		require.NotEmpty(t, cfg.Model)
	})

	t.Run("returns error for unknown category", func(t *testing.T) {
		resolver := NewCategoryResolver(nil)
		_, err := resolver.Resolve("unknown-category")
		require.Error(t, err)
	})

	t.Run("user config overrides default", func(t *testing.T) {
		overrides := map[string]CategoryConfig{
			"quick": {Model: "gpt-4.1", Provider: "openai"},
		}
		resolver := NewCategoryResolver(overrides)
		cfg, err := resolver.Resolve("quick")
		require.NoError(t, err)
		require.Equal(t, "gpt-4.1", cfg.Model)
		require.Equal(t, "openai", cfg.Provider)
	})

	t.Run("falls back to default if not overridden", func(t *testing.T) {
		overrides := map[string]CategoryConfig{
			"summarise": {Model: "gpt-3.5", Provider: "openai"},
		}
		resolver := NewCategoryResolver(overrides)
		cfg, err := resolver.Resolve("quick")
		require.NoError(t, err)
		require.NotEmpty(t, cfg.Model)
	})
}
