package eventbus

import (
	"reflect"
	"sync"
)

// EventHandler defines the function signature for event handlers.
//
// Expected: used as a callback for events.
// Returns: none.
// Side effects: handler may mutate external state.
type EventHandler func(event any)

// EventBus provides a concurrency-safe publish/subscribe event system for plugins.
//
// Expected:
//   - Allows multiple handlers per event type.
//   - Handlers can be added and removed safely at runtime.
//   - Publishing an event delivers it to all current handlers for that type.
//   - No data races or panics under concurrent use.
//
// Returns: a ready-to-use EventBus.
// Side effects: none.
type EventBus struct {
	mu       sync.RWMutex
	handlers map[string][]EventHandler
}

// NewEventBus creates a new, empty EventBus.
//
// Expected:
//   - Returns a ready-to-use EventBus with no handlers.
//
// Returns: pointer to a new EventBus.
// Side effects: none.
func NewEventBus() *EventBus {
	return &EventBus{
		handlers: make(map[string][]EventHandler),
	}
}

// Subscribe adds a handler for the given event type.
//
// Expected:
//   - Handler is called for every event of the given type published after subscription.
//   - Safe to call concurrently.
//
// Returns: none.
// Side effects: mutates handlers map.
func (b *EventBus) Subscribe(eventType string, handler EventHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], handler)
}

// Unsubscribe removes a handler for the given event type.
//
// Expected:
//   - Handler is removed and will not receive future events.
//   - Safe to call concurrently.
//
// Returns: none.
// Side effects: mutates handlers map.
func (b *EventBus) Unsubscribe(eventType string, handler EventHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	handlers := b.handlers[eventType]
	for i, h := range handlers {
		if getFuncPointer(h) == getFuncPointer(handler) {
			b.handlers[eventType] = append(handlers[:i], handlers[i+1:]...)
			break
		}
	}
}

// Publish delivers an event to all handlers for the given event type.
//
// Expected:
//   - Calls all handlers registered for eventType at the time of publish.
//   - Safe to call concurrently with Subscribe/Unsubscribe.
//
// Returns: none.
// Side effects: invokes handler functions, which may mutate external state.
func (b *EventBus) Publish(eventType string, event any) {
	b.mu.RLock()
	handlers := append([]EventHandler(nil), b.handlers[eventType]...)
	b.mu.RUnlock()
	for _, handler := range handlers {
		handler(event)
	}
}

// getFuncPointer returns the pointer value of a function for comparison.
// Used to compare handler functions for unsubscribe.
//
// Expected: called internally by Unsubscribe.
// Returns: uintptr pointer value of the function.
// Side effects: none.
func getFuncPointer(fn EventHandler) uintptr {
	return reflect.ValueOf(fn).Pointer()
}
