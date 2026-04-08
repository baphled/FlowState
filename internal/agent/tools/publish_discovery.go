// Package tools provides agent tools for FlowState.
package tools

import "errors"

// PublishDiscoveryInput defines the input for publishing a discovery.
type PublishDiscoveryInput struct {
	Kind     string
	Summary  string
	Priority string
	Details  string
	Affects  string
	Evidence string
}

// DiscoveryStore defines the interface for publishing discoveries.
type DiscoveryStore interface {
	// Publish stores a discovery and returns its ID or an error.
	Publish(input PublishDiscoveryInput) (string, error)
}

// PublishDiscoveryTool provides the tool for publishing discoveries.
type PublishDiscoveryTool struct {
	store DiscoveryStore
}

// NewPublishDiscoveryTool creates a new PublishDiscoveryTool.
//
// Expected:
//   - store implements DiscoveryStore.
//
// Returns:
//   - A *PublishDiscoveryTool configured with the given store.
//
// Side effects:
//   - None.
func NewPublishDiscoveryTool(store DiscoveryStore) *PublishDiscoveryTool {
	return &PublishDiscoveryTool{store: store}
}

// Run publishes a discovery using the provided input.
//
// Expected:
//   - input.Kind, input.Summary, input.Priority must be non-empty strings.
//
// Returns:
//   - The published discovery ID, or error if validation fails or store.Publish fails.
//
// Side effects:
//   - Publishes the discovery to the configured DiscoveryStore.
func (t *PublishDiscoveryTool) Run(input PublishDiscoveryInput) (string, error) {
	// Validate required fields (AC2)
	if input.Kind == "" {
		return "", errors.New("kind is required")
	}
	if input.Summary == "" {
		return "", errors.New("summary is required")
	}
	if input.Priority == "" {
		return "", errors.New("priority is required")
	}

	// Call store.Publish and return result (AC3, AC4)
	return t.store.Publish(input)
}
