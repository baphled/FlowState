package engine

import (
	"errors"

	"github.com/baphled/flowstate/internal/provider"
)

var errUnknownCategory = errors.New("unknown category")

var abstractDescriptors = map[string]bool{
	"fast":      true,
	"reasoning": true,
	"vision":    true,
	"balanced":  true,
}

// ModelLister is a function that returns the list of currently available models.
type ModelLister func() ([]provider.Model, error)

// isAbstractDescriptor checks if a model name is an abstract descriptor.
//
// Expected:
//   - model is a non-empty string.
//
// Returns:
//   - true if model is in the set of abstract descriptors, false otherwise.
//
// Side effects:
//   - None.
func isAbstractDescriptor(model string) bool {
	return abstractDescriptors[model]
}

// CategoryResolver maps category names to model routing configuration.
type CategoryResolver struct {
	overrides   map[string]CategoryConfig
	modelLister ModelLister
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

// WithModelLister sets a function used to fetch live model IDs for abstract descriptor resolution.
//
// Expected:
//   - fn is a non-nil function returning available provider models.
//
// Returns:
//   - The CategoryResolver for chaining.
//
// Side effects:
//   - Replaces any previously configured model lister.
func (r *CategoryResolver) WithModelLister(fn ModelLister) *CategoryResolver {
	r.modelLister = fn
	return r
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
//   - If modelLister is configured and resolves abstract descriptors, updates cfg.Model to a real ID.
func (r *CategoryResolver) Resolve(category string) (CategoryConfig, error) {
	merged := DefaultCategoryRouting()
	for k, v := range r.overrides {
		merged[k] = v
	}
	cfg, ok := merged[category]
	if !ok {
		return CategoryConfig{}, errUnknownCategory
	}
	if r.modelLister != nil && isAbstractDescriptor(cfg.Model) {
		if models, err := r.modelLister(); err == nil && len(models) > 0 {
			cfg.Model = r.resolveModel(cfg.Model, models)
		}
	}
	return cfg, nil
}

// resolveModel picks a real model ID from the available list based on the descriptor.
//
// Expected:
//   - descriptor is one of the abstract descriptors or a non-abstract model ID.
//   - models is a non-empty slice.
//
// Returns:
//   - A real model ID selected according to the descriptor's strategy.
//
// Side effects:
//   - None.
func (r *CategoryResolver) resolveModel(descriptor string, models []provider.Model) string {
	if len(models) == 0 || !isAbstractDescriptor(descriptor) {
		return descriptor
	}
	switch descriptor {
	case "fast":
		return pickSmallest(models).ID
	case "reasoning":
		return pickLargest(models).ID
	case "vision":
		return models[0].ID
	default:
		return pickMedian(models).ID
	}
}

// pickSmallest returns the model with the smallest context length.
//
// Expected:
//   - models is a non-empty slice.
//
// Returns:
//   - The model with the smallest ContextLength, or the first model if all are 0.
//
// Side effects:
//   - None.
func pickSmallest(models []provider.Model) provider.Model {
	if len(models) == 0 {
		return provider.Model{}
	}
	smallest := models[0]
	for _, m := range models[1:] {
		if m.ContextLength > 0 && (smallest.ContextLength == 0 || m.ContextLength < smallest.ContextLength) {
			smallest = m
		}
	}
	return smallest
}

// pickLargest returns the model with the largest context length.
//
// Expected:
//   - models is a non-empty slice.
//
// Returns:
//   - The model with the largest ContextLength.
//
// Side effects:
//   - None.
func pickLargest(models []provider.Model) provider.Model {
	if len(models) == 0 {
		return provider.Model{}
	}
	largest := models[0]
	for _, m := range models[1:] {
		if m.ContextLength > largest.ContextLength {
			largest = m
		}
	}
	return largest
}

// pickMedian returns the model at the median position after sorting by context length.
//
// Expected:
//   - models is a non-empty slice.
//
// Returns:
//   - The model at the median index of the context-length-sorted list.
//
// Side effects:
//   - None.
func pickMedian(models []provider.Model) provider.Model {
	if len(models) == 0 {
		return provider.Model{}
	}
	if len(models) == 1 {
		return models[0]
	}
	sortedByContext := make([]provider.Model, len(models))
	copy(sortedByContext, models)
	for i := 0; i < len(sortedByContext); i++ {
		for j := i + 1; j < len(sortedByContext); j++ {
			if sortedByContext[j].ContextLength < sortedByContext[i].ContextLength {
				sortedByContext[i], sortedByContext[j] = sortedByContext[j], sortedByContext[i]
			}
		}
	}
	return sortedByContext[len(sortedByContext)/2]
}
