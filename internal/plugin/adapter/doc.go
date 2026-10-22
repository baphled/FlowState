// Package adapter provides the PluginEventAdapter for translating internal FlowState
// events to public JSON payloads for external plugin consumption.
//
// The adapter bridges the internal event system with external plugins by:
//   - Resolving namespace wildcard patterns (e.g. "provider.*") against the event
//     catalog into exact internal topic sets at registration time.
//   - Subscribing to resolved topics on the EventBus.
//   - Translating matched internal events to JSON-safe PublicEvent payloads.
//   - Routing PublicEvent values only to plugins that subscribed to the matched topic.
package adapter
