// Package external — inbound.go handles inbound JSON-RPC notifications and
// subscription requests from external plugins.
package external

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/baphled/flowstate/internal/plugin/adapter"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
)

// InboundHandler processes inbound JSON-RPC notifications and requests from
// external plugins, enforcing rate limits and topic namespace protection.
type InboundHandler struct {
	pluginName string
	bus        *eventbus.EventBus
	adapter    *adapter.PluginEventAdapter
	mu         sync.Mutex
	tokens     float64
	lastRefill time.Time
	maxRate    float64
}

// NewInboundHandler creates a handler for the named plugin with a default rate
// limit of 10 events per second.
//
// Expected:
//   - pluginName is a non-empty unique identifier for the plugin.
//   - bus is a non-nil, initialised EventBus.
//   - adp is a non-nil, initialised PluginEventAdapter.
//
// Returns: pointer to a new InboundHandler ready to process inbound events.
//
// Side effects: None.
func NewInboundHandler(pluginName string, bus *eventbus.EventBus, adp *adapter.PluginEventAdapter) *InboundHandler {
	return &InboundHandler{
		pluginName: pluginName,
		bus:        bus,
		adapter:    adp,
		tokens:     10,
		lastRefill: time.Now(),
		maxRate:    10,
	}
}

// HandleNotification processes a notifications/event notification from an external plugin.
// Publishes to the bus as ext.{pluginName}.{eventName}.
//
// Expected:
//   - eventName must not start with "ext." — that prefix is reserved for the bus routing scheme.
//   - data is the raw JSON payload to publish.
//
// Returns: an error if the event name starts with "ext." or the rate limit is exceeded; nil on success.
//
// Side effects: Publishes an event to the EventBus; logs a warning on rate limit exceed.
func (h *InboundHandler) HandleNotification(eventName string, data json.RawMessage) error {
	if strings.HasPrefix(eventName, "ext.") {
		return fmt.Errorf("event name must not start with 'ext.': %q", eventName)
	}

	if !h.consumeToken() {
		slog.Warn("inbound event rate limit exceeded", "plugin", h.pluginName, "event", eventName)
		return fmt.Errorf("rate limit exceeded for plugin %q", h.pluginName)
	}

	topic := "ext." + h.pluginName + "." + eventName
	h.bus.Publish(topic, data)

	return nil
}

// HandleSubscribe processes an events/subscribe request by registering the
// plugin's desired event patterns and handler via the adapter.
//
// Expected:
//   - patterns contains one or more exact topic strings or "namespace.*" wildcards.
//   - handler is called once per matched event with the translated PublicEvent payload.
//
// Returns: an error if any pattern matches no catalog entries; nil on success.
//
// Side effects: registers subscriptions on the EventBus via the adapter.
func (h *InboundHandler) HandleSubscribe(patterns []string, handler func(adapter.PublicEvent)) error {
	return h.adapter.RegisterPluginSubscription(h.pluginName, patterns, handler)
}

// HandleUnsubscribe removes the plugin's subscriptions from the adapter.
//
// Expected: the plugin was previously registered via HandleSubscribe.
//
// Returns: none.
//
// Side effects: removes plugin subscriptions and handler from the adapter.
func (h *InboundHandler) HandleUnsubscribe() {
	h.adapter.UnregisterPlugin(h.pluginName)
}

// consumeToken implements token bucket rate limiting.
// Returns true if a token was consumed, false if the bucket is empty.
//
// Expected: called under no external lock — acquires its own mutex.
//
// Returns: true if an event token was consumed; false if the bucket is exhausted.
//
// Side effects: mutates tokens and lastRefill fields under mutex.
func (h *InboundHandler) consumeToken() bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(h.lastRefill).Seconds()
	h.tokens = min(h.maxRate, h.tokens+elapsed*h.maxRate)
	h.lastRefill = now

	if h.tokens < 1 {
		return false
	}

	h.tokens--

	return true
}
