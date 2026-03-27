package engine

import "errors"

var errUnknownCategory = errors.New("unknown category")

// CategoryResolver maps category names to model routing configuration.
type CategoryResolver struct {
	overrides map[string]CategoryConfig
}

// NewCategoryResolver creates a CategoryResolver with optional user overrides.
//
// Expected:
//   - overrides may be nil to use only hardcoded defaults.
//
// Returns:
//   - A CategoryResolver that merges overrides on top of defaults.
//
// Side effects:
//   - None.
func NewCategoryResolver(overrides map[string]CategoryConfig) *CategoryResolver {
	return &CategoryResolver{overrides: overrides}
}

// Resolve returns the CategoryConfig for the given category name.
//
// Expected:
//   - category is a non-empty string identifying the routing category.
//
// Returns:
//   - The merged CategoryConfig when the category is known.
//   - errUnknownCategory when the category is not found in defaults or overrides.
//
// Side effects:
//   - None.
func (r *CategoryResolver) Resolve(category string) (CategoryConfig, error) {
	merged := DefaultCategoryRouting()
	for k, v := range r.overrides {
		merged[k] = v
	}
	cfg, ok := merged[category]
	if !ok {
		return CategoryConfig{}, errUnknownCategory
	}
	return cfg, nil
}
