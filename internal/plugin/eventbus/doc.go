// Package eventbus provides a concurrency-safe event bus for plugin communication.
//
// Responsibilities:
//   - Allow plugins to subscribe to and publish events
//   - Ensure thread-safe subscription, unsubscription, and event delivery
//   - Support multiple event types and handlers
//   - Guarantee no data races or panics under concurrent use
//
// This package is used internally by the plugin system to decouple event producers and consumers.
package eventbus
