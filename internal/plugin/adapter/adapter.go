// Package adapter provides the PluginEventAdapter for translating internal FlowState
// events to public JSON payloads for external plugin consumption.
package adapter

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
)

// PublicEvent is the JSON-safe payload sent to external plugins.
// It contains no internal Go type names — only serialisable data.
type PublicEvent struct {
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Version   string          `json:"version"`
	Data      json.RawMessage `json:"data"`
	Origin    string          `json:"origin,omitempty"`
}

// PluginEventAdapter translates internal events to PublicEvent payloads
// and routes them to subscribed external plugins.
type PluginEventAdapter struct {
	mu            sync.RWMutex
	bus           *eventbus.EventBus
	subscriptions map[string]map[string]struct{}
	handlers      map[string]func(PublicEvent)
}

// NewPluginEventAdapter creates a new adapter backed by the given EventBus.
//
// Expected:
//   - bus is a non-nil, initialised EventBus.
//
// Returns: pointer to a new PluginEventAdapter with empty subscription maps.
// Side effects: none.
func NewPluginEventAdapter(bus *eventbus.EventBus) *PluginEventAdapter {
	return &PluginEventAdapter{
		bus:           bus,
		subscriptions: make(map[string]map[string]struct{}),
		handlers:      make(map[string]func(PublicEvent)),
	}
}

// RegisterPluginSubscription registers a plugin's desired event patterns and handler.
// Patterns may use namespace wildcards (e.g. "provider.*").
// Returns an error if any pattern does not match any catalog entry.
//
// Expected:
//   - pluginName is a unique identifier for the subscribing plugin.
//   - patterns contains one or more exact topic strings or "namespace.*" wildcards.
//   - handler is called once per matched event, with the translated PublicEvent payload.
//
// Returns: an error if any pattern matches no catalog entries; nil on success.
// Side effects: subscribes to the EventBus for each resolved topic.
func (a *PluginEventAdapter) RegisterPluginSubscription(pluginName string, patterns []string, handler func(PublicEvent)) error {
	topics, err := resolvePatterns(patterns)
	if err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	topicSet := make(map[string]struct{}, len(topics))
	for _, t := range topics {
		topicSet[t] = struct{}{}
	}
	a.subscriptions[pluginName] = topicSet
	a.handlers[pluginName] = handler

	for _, topic := range topics {
		t := topic
		a.bus.Subscribe(t, func(event any) {
			a.dispatch(t, event)
		})
	}
	return nil
}

// resolvePatterns resolves namespace patterns against the event catalog.
// "provider.*" matches all events with a "provider." prefix.
// Returns an error if no catalog entries match a pattern.
//
// Expected:
//   - patterns is a non-empty slice of exact topics or "namespace.*" wildcards.
//
// Returns: slice of resolved exact topic strings; error if any pattern is unmatched.
// Side effects: none.
func resolvePatterns(patterns []string) ([]string, error) {
	var resolved []string
	for _, pattern := range patterns {
		matched := false
		if strings.HasSuffix(pattern, ".*") {
			prefix := strings.TrimSuffix(pattern, "*")
			for i := range events.Catalog {
				if strings.HasPrefix(events.Catalog[i].Topic, prefix) {
					resolved = append(resolved, events.Catalog[i].Topic)
					matched = true
				}
			}
		} else {
			for i := range events.Catalog {
				if events.Catalog[i].Topic == pattern {
					resolved = append(resolved, events.Catalog[i].Topic)
					matched = true
					break
				}
			}
		}
		if !matched {
			return nil, fmt.Errorf("no catalog entries match pattern %q", pattern)
		}
	}
	return resolved, nil
}

// dispatch translates an internal event to a PublicEvent and routes it to
// all plugins that subscribed to the given topic.
//
// Expected:
//   - topic is the exact EventBus topic for which event was published.
//   - event is the internal Go event struct emitted by the engine.
//
// Side effects: calls plugin handler functions with the serialised PublicEvent.
func (a *PluginEventAdapter) dispatch(topic string, event any) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	pub := PublicEvent{
		Type:      topic,
		Timestamp: time.Now(),
		Version:   "1",
		Data:      json.RawMessage(data),
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	for pluginName, topicSet := range a.subscriptions {
		if _, ok := topicSet[topic]; ok {
			if h, ok := a.handlers[pluginName]; ok {
				h(pub)
			}
		}
	}
}
