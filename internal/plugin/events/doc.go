// Package events provides typed event implementations for the plugin EventBus.
//
// Responsibilities:
//   - Defines BaseEvent with event type and timestamp
//   - Provides strongly typed SessionEvent, ToolEvent, ProviderEvent structs
//   - Ensures all event types are suitable for use with the EventBus
//   - Constructors for each event type
//
// This package is internal to the plugin system and not intended for external use.
package events
