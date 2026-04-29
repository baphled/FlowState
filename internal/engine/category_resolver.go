package engine

import (
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/provider"
)

var errUnknownCategory = errors.New("unknown category")

// CategoryModelSwapEventType is the event-bus topic the resolver swap
// notification publishes under. The TUI's chat intent subscribes to
// this topic to render an auto-promotion toast in the activity
// overlay; CLI surfaces can subscribe similarly. Pulled out as an
// exported constant so publishers (App.buildCategorySwapNotifier) and
// subscribers reference the same string.
const CategoryModelSwapEventType = "category.model_swap"

// CategoryModelSwap describes the resolver's decision to substitute a
// different model for an abstract descriptor's first-choice pick when
// the first choice fell foul of the tool-capability allow/deny lists.
// Surfaced via the SwapNotifier so the user sees which agent got
// upgraded and why — silent swaps are the failure mode this whole
// feature exists to prevent.
type CategoryModelSwap struct {
	// Category is the routing category that triggered the resolution
	// (e.g. "quick", "deep"). Populated by Resolve at notify time.
	Category string

	// Original is the model the descriptor's strategy
	// (smallest/largest/median) would have chosen against the unfiltered
	// model list — i.e. what the user gets without this feature.
	Original string

	// Chosen is the model the resolver actually returned after applying
	// the capability filter. Always non-empty when the swap fires;
	// equals Original means "no swap" and the notifier is not invoked.
	Chosen string

	// Reason is a short human-readable explanation of why Original was
	// excluded. Examples: `matches tool-incapable pattern "claude-haiku*"`,
	// `not in tool-capable allowlist`. Surfaced verbatim in the
	// notification event so reviewers don't have to re-derive the
	// matching pattern.
	Reason string
}

// SwapNotifier is invoked when capability filtering changed the
// resolver's chosen model. Implementations typically log + publish on
// an event bus so the activity pane / CLI surface picks up the swap.
// nil is allowed (the resolver no-ops the notify path) so test wiring
// stays tiny.
type SwapNotifier func(CategoryModelSwap)

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

	// toolCapableModels and toolIncapableModels mirror the cluster-wide
	// allow/deny lists DelegateTool consults at delegation time. Threading
	// them down to the resolver lets us pre-empt the gate failure: when
	// the abstract-descriptor's first-choice pick is denied, we promote
	// to the next-best capable model from the same provider list and
	// notify, so a misconfigured agent gets auto-upgraded instead of
	// silently failing every delegation. Empty/nil means "skip the
	// capability filter" — no swap, no notification.
	toolCapableModels   []string
	toolIncapableModels []string

	// notifier is invoked when capability filtering changed which model
	// the descriptor resolved to. nil is allowed; the no-op path keeps
	// test wiring tiny.
	notifier SwapNotifier
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

// WithToolCapability installs the cluster-wide tool-capability
// allow/deny lists on the resolver. When set, descriptor resolution
// filters the candidate model list to capable models BEFORE applying
// the smallest/largest/median strategy — the gate downstream of
// delegation never sees a denied model in the happy path.
//
// Expected:
//   - allow / deny use the same glob shapes as IsToolCapableModel
//     (`prefix*`, `*suffix`, `prefix*suffix`, literal). May be nil.
//
// Returns:
//   - The CategoryResolver for chaining.
//
// Side effects:
//   - Replaces any previously configured allow/deny patterns.
func (r *CategoryResolver) WithToolCapability(allow, deny []string) *CategoryResolver {
	r.toolCapableModels = allow
	r.toolIncapableModels = deny
	return r
}

// WithSwapNotifier installs the callback fired when capability
// filtering changed the resolver's chosen model. App-level wiring
// publishes a `category.model_swap` event on the engine bus so the
// activity pane shows the upgrade. Tests pass an in-memory recorder.
//
// Expected:
//   - fn may be nil to disable notifications.
//
// Returns:
//   - The CategoryResolver for chaining.
//
// Side effects:
//   - Replaces any previously installed notifier.
func (r *CategoryResolver) WithSwapNotifier(fn SwapNotifier) *CategoryResolver {
	r.notifier = fn
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
			chosen, swap := r.resolveModelWithCapability(cfg.Model, models)
			cfg.Model = chosen
			if swap != nil && r.notifier != nil {
				swap.Category = category
				r.notifier(*swap)
			}
		}
	}
	return cfg, nil
}

// resolveModelWithCapability picks a model ID from the available list
// using the descriptor's strategy, applying the capability allow/deny
// filter where one is configured. Returns the chosen ID and, when the
// filter changed the result, a populated *CategoryModelSwap describing
// what was dropped and why.
//
// Capability is "soft" at this layer: when the filter would empty the
// candidate list (e.g. the user has zero capable models in the active
// provider's catalog), we fall through to the unfiltered pick rather
// than returning empty. The downstream DelegateTool gate still
// fail-closes — this layer only OPTIMISES the happy path.
//
// Expected:
//   - descriptor is one of the abstract descriptors or a non-abstract
//     model ID. A non-abstract descriptor short-circuits to itself.
//   - models is a non-empty slice (caller has already guarded).
//
// Returns:
//   - The chosen model ID.
//   - A non-nil *CategoryModelSwap when capability filtering swapped
//     the original pick; nil when no swap occurred.
//
// Side effects:
//   - None.
func (r *CategoryResolver) resolveModelWithCapability(descriptor string, models []provider.Model) (string, *CategoryModelSwap) {
	if len(models) == 0 || !isAbstractDescriptor(descriptor) {
		return descriptor, nil
	}

	original := pickByDescriptor(descriptor, models).ID

	if !r.capabilityActive() {
		return original, nil
	}

	capable := r.filterCapable(models)
	if len(capable) == 0 {
		return original, nil
	}

	chosen := pickByDescriptor(descriptor, capable).ID
	if chosen == "" || chosen == original {
		return original, nil
	}

	return chosen, &CategoryModelSwap{
		Original: original,
		Chosen:   chosen,
		Reason:   r.denyReason(original),
	}
}

// resolveModel preserves the pre-capability behaviour for callers that
// pre-date WithToolCapability/WithSwapNotifier (currently only the
// engine-internal call path that hits Resolve, but kept for symmetry
// with the rest of the resolver API). Equivalent to
// resolveModelWithCapability when no allow/deny lists are configured.
//
// Expected:
//   - descriptor is one of the abstract descriptors or a non-abstract
//     model ID.
//   - models is a non-empty slice.
//
// Returns:
//   - A real model ID selected according to the descriptor's strategy.
//
// Side effects:
//   - None.
func (r *CategoryResolver) resolveModel(descriptor string, models []provider.Model) string {
	chosen, _ := r.resolveModelWithCapability(descriptor, models)
	return chosen
}

// pickByDescriptor applies the abstract descriptor's strategy to the
// model slice. Pulled out of resolveModel so the capability filter can
// run the same strategy over the filtered subset without re-encoding
// the switch.
//
// Expected:
//   - descriptor is one of the abstract descriptors. Non-abstract
//     descriptors are not expected here; the surrounding caller
//     short-circuits before this is reached.
//   - models is non-empty.
//
// Returns:
//   - The picked model.
//
// Side effects:
//   - None.
func pickByDescriptor(descriptor string, models []provider.Model) provider.Model {
	if len(models) == 0 {
		return provider.Model{}
	}
	switch descriptor {
	case "fast":
		return pickSmallest(models)
	case "reasoning":
		return pickLargest(models)
	case "vision":
		return models[0]
	default:
		return pickMedian(models)
	}
}

// capabilityActive reports whether at least one allow/deny pattern is
// configured. Without one, the filter must be a no-op so existing
// callers (no WithToolCapability) keep their pre-feature behaviour.
//
// Returns:
//   - true when either list has at least one entry.
//
// Side effects:
//   - None.
func (r *CategoryResolver) capabilityActive() bool {
	return len(r.toolCapableModels) > 0 || len(r.toolIncapableModels) > 0
}

// filterCapable returns the subset of models that pass
// IsToolCapableModel against the configured allow/deny lists. Order is
// preserved so the descriptor strategies (smallest/largest/median)
// keep their stable-sort semantics within the filtered subset.
//
// Expected:
//   - r.capabilityActive() is true (caller guards).
//
// Returns:
//   - The capable subset; possibly empty when nothing passes.
//
// Side effects:
//   - None.
func (r *CategoryResolver) filterCapable(models []provider.Model) []provider.Model {
	out := make([]provider.Model, 0, len(models))
	for _, m := range models {
		if IsToolCapableModel(m.Provider, m.ID, r.toolCapableModels, r.toolIncapableModels) {
			out = append(out, m)
		}
	}
	return out
}

// denyReason returns a short human-readable explanation for why the
// model was dropped from the capable subset. Reports the FIRST matched
// deny pattern (deny takes precedence over allow), and falls back to a
// "not in allowlist" hint when allow is the only constraint.
//
// Expected:
//   - model is the rejected model ID.
//
// Returns:
//   - A non-empty reason string. Empty string means "no obvious
//     reason" — surfaces only when the function is called against a
//     model that actually passes capability (which the caller should
//     not do).
//
// Side effects:
//   - None.
func (r *CategoryResolver) denyReason(model string) string {
	for _, pat := range r.toolIncapableModels {
		if matchesPattern(model, pat) {
			return fmt.Sprintf("matches tool-incapable pattern %q", pat)
		}
	}
	if len(r.toolCapableModels) > 0 && !matchesAnyPattern(model, r.toolCapableModels) {
		return "not in tool-capable allowlist"
	}
	return ""
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
