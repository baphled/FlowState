// Package events — catalog.go provides the canonical compile-time catalog of all
// event types in the FlowState event system.
//
// This file is machine-readable metadata intended for use by tooling, documentation
// generators, and downstream packages (e.g. T11 validation, T13 documentation).
// It is NOT a runtime registry — no dynamic dispatch or registration happens here.
//
// Rules:
//   - Do NOT add event versioning negotiation here.
//   - Do NOT modify topic strings — they must match the constants in types.go exactly.
//   - When a new event is introduced, add a corresponding entry to Catalog.
//   - When an event is deprecated, change its Status to StatusDeprecated and update Notes.
package events

// EventScope indicates whether an event is intended for internal use only or is
// part of the public plugin API.
type EventScope string

const (
	// ScopeInternal marks events consumed only within the FlowState engine and
	// built-in plugins (eventlogger, sessionrecorder).
	ScopeInternal EventScope = "internal"

	// ScopePublic marks events that external plugins may subscribe to or publish.
	ScopePublic EventScope = "public"
)

// EventStatus indicates the lifecycle stage of an event type.
type EventStatus string

const (
	// StatusActive is the normal state for an event type that is in use.
	StatusActive EventStatus = "active"

	// StatusTransitional marks an event type that is in the process of being
	// replaced or restructured. Subscribers should plan for migration.
	StatusTransitional EventStatus = "transitional"

	// StatusDeprecated marks an event type that is no longer recommended.
	// It may still be published during a transition window but will be removed.
	StatusDeprecated EventStatus = "deprecated"
)

// EventCatalogEntry describes a single event type in the FlowState event system.
//
// Each entry maps a topic constant to its Go struct, lifecycle status, scope, and
// the packages that publish or subscribe to it. The EventType field records the
// value returned by the event's EventType() method, which may differ from Topic
// for legacy events that predate the structured-topic naming scheme.
type EventCatalogEntry struct {
	// Topic is the string value of the event topic constant (e.g. "session.created").
	// This is the value passed to bus.Publish and bus.Subscribe.
	Topic string

	// Constant is the Go constant name used to refer to this topic (e.g. "EventSessionCreated").
	Constant string

	// EventType is the value returned by the event struct's EventType() method.
	// For most events this matches Topic, but for legacy events it may be a shorter
	// prefix (e.g. "session" for both "session.created" and "session.ended").
	// See the Notes field for divergence details.
	EventType string

	// Struct is the Go type name of the event payload (e.g. "SessionEvent").
	// For external plugin events that have no dedicated struct, this is empty.
	Struct string

	// Publishers lists the source files or packages that call bus.Publish for
	// this topic. "(not yet wired)" indicates the publisher is planned but absent.
	Publishers []string

	// Subscribers lists the source files or packages that call bus.Subscribe for
	// this topic. "(T14 will add subscribers)" indicates planned future wiring.
	Subscribers []string

	// Scope indicates whether the event is internal-only or part of the public API.
	Scope EventScope

	// Status reflects the lifecycle stage: active, transitional, or deprecated.
	Status EventStatus

	// Delivery describes the dispatch guarantee. All FlowState events use
	// fire-and-forget semantics.
	Delivery string

	// Notes records divergences, deprecation reasons, or other relevant context.
	Notes string
}

// NamespaceRules documents the topic namespace policy enforced at the inbound
// plugin boundary (internal/plugin/external.InboundHandler).
//
// External plugins publish events via the JSON-RPC notifications/event method.
// The inbound handler enforces the following rules:
//
//   - The "ext.*" namespace is reserved exclusively for external plugin events.
//     External plugins publish as "ext.{plugin-name}.{event-name}".
//     A plugin named "my-plugin" sending "ping" produces the topic
//     "ext.my-plugin.ping" on the EventBus.
//
//   - All other topic prefixes (agent.*, background.*, context.*, plugin.*,
//     prompt.*, provider.*, session.*, tool.*) are reserved for internal
//     FlowState events. External plugins CANNOT publish directly to these topics.
//
//   - Attempting to use an "ext.*" prefixed name is also rejected to prevent
//     double-prefixing (e.g. "ext.foo" → would produce "ext.plugin.ext.foo").
//
// Enforcement: internal/plugin/external.isInternalTopic checks the eventName
// against every Topic field in Catalog before allowing the publish to proceed.
// This check lives at the adapter boundary, NOT inside the EventBus itself.
var NamespaceRules = struct {
	ExternalPrefix     string
	InternalNamespaces []string
}{
	ExternalPrefix: "ext.",
	InternalNamespaces: []string{
		"agent.",
		"background.",
		"context.",
		"plugin.",
		"prompt.",
		"provider.",
		"session.",
		"tool.",
	},
}

// Catalog is the canonical list of all event types in the FlowState event system.
//
// Entries are ordered by topic prefix grouping: agent, background, context,
// plugin, prompt, provider, session, tool.
var Catalog = []EventCatalogEntry{
	{
		Topic:       EventAgentSwitched,
		Constant:    "EventAgentSwitched",
		EventType:   "agent.switched",
		Struct:      "AgentSwitchedEvent",
		Publishers:  []string{"engine.go"},
		Subscribers: []string{"eventlogger", "sessionrecorder"},
		Scope:       ScopeInternal,
		Status:      StatusActive,
		Delivery:    "fire-and-forget",
	},
	{
		Topic:       EventBackgroundTaskStarted,
		Constant:    "EventBackgroundTaskStarted",
		EventType:   "background.task.started",
		Struct:      "BackgroundTaskStartedEvent",
		Publishers:  []string{"engine/background.go"},
		Subscribers: []string{"eventlogger", "sessionrecorder"},
		Scope:       ScopeInternal,
		Status:      StatusActive,
		Delivery:    "fire-and-forget",
	},
	{
		Topic:       EventBackgroundTaskCompleted,
		Constant:    "EventBackgroundTaskCompleted",
		EventType:   "background.task.completed",
		Struct:      "BackgroundTaskCompletedEvent",
		Publishers:  []string{"engine/background.go"},
		Subscribers: []string{"eventlogger", "sessionrecorder"},
		Scope:       ScopeInternal,
		Status:      StatusActive,
		Delivery:    "fire-and-forget",
	},
	{
		Topic:       EventBackgroundTaskFailed,
		Constant:    "EventBackgroundTaskFailed",
		EventType:   "background.task.failed",
		Struct:      "BackgroundTaskFailedEvent",
		Publishers:  []string{"engine/background.go"},
		Subscribers: []string{"eventlogger", "sessionrecorder"},
		Scope:       ScopeInternal,
		Status:      StatusActive,
		Delivery:    "fire-and-forget",
	},
	{
		Topic:       EventContextWindowBuilt,
		Constant:    "EventContextWindowBuilt",
		EventType:   "context.window",
		Struct:      "ContextWindowEvent",
		Publishers:  []string{"engine.go"},
		Subscribers: []string{"eventlogger", "sessionrecorder"},
		Scope:       ScopeInternal,
		Status:      StatusActive,
		Delivery:    "fire-and-forget",
		Notes: "DIVERGENCE: EventType() returns \"context.window\" but topic is \"context.window.built\"." +
			" Do not change EventType() — it affects the serialised JSONL format.",
	},
	{
		Topic:       EventPluginEvent,
		Constant:    "EventPluginEvent",
		EventType:   "plugin.event",
		Struct:      "",
		Publishers:  []string{"(external plugins)"},
		Subscribers: []string{"app.go (dispatcher)"},
		Scope:       ScopePublic,
		Status:      StatusActive,
		Delivery:    "fire-and-forget",
		Notes:       "No dedicated struct — external plugins publish arbitrary payloads under this topic.",
	},
	{
		Topic:       EventPromptGenerated,
		Constant:    "EventPromptGenerated",
		EventType:   "prompt",
		Struct:      "PromptEvent",
		Publishers:  []string{"engine.go"},
		Subscribers: []string{"eventlogger", "sessionrecorder"},
		Scope:       ScopeInternal,
		Status:      StatusActive,
		Delivery:    "fire-and-forget",
		Notes: "DIVERGENCE: EventType() returns \"prompt\" but topic is \"prompt.generated\"." +
			" Do not change EventType() — it affects the serialised JSONL format.",
	},
	{
		Topic:       EventProviderError,
		Constant:    "EventProviderError",
		EventType:   "provider.error",
		Struct:      "ProviderErrorEvent",
		Publishers:  []string{"engine.go", "stream_hook.go"},
		Subscribers: []string{"detector.go", "eventlogger", "sessionrecorder"},
		Scope:       ScopeInternal,
		Status:      StatusActive,
		Delivery:    "fire-and-forget",
	},
	{
		Topic:       EventProviderRateLimited,
		Constant:    "EventProviderRateLimited",
		EventType:   "provider",
		Struct:      "ProviderEvent",
		Publishers:  []string{"detector.go"},
		Subscribers: []string{"app.go (rate-limit logger)", "eventlogger", "sessionrecorder"},
		Scope:       ScopeInternal,
		Status:      StatusTransitional,
		Delivery:    "fire-and-forget",
		Notes: "DIVERGENCE: EventType() returns \"provider\" but topic is \"provider.rate_limited\"." +
			" ProviderEvent is the generic event type used for re-publishing;" +
			" marked transitional pending migration to a dedicated type.",
	},
	{
		Topic:       EventProviderRequest,
		Constant:    "EventProviderRequest",
		EventType:   "provider.request",
		Struct:      "ProviderRequestEvent",
		Publishers:  []string{"engine.go"},
		Subscribers: []string{"eventlogger", "sessionrecorder"},
		Scope:       ScopeInternal,
		Status:      StatusActive,
		Delivery:    "fire-and-forget",
	},
	{
		Topic:       EventProviderRequestRetry,
		Constant:    "EventProviderRequestRetry",
		EventType:   "provider.request.retry",
		Struct:      "ProviderRequestRetryEvent",
		Publishers:  []string{"engine.go"},
		Subscribers: []string{"eventlogger", "sessionrecorder"},
		Scope:       ScopeInternal,
		Status:      StatusActive,
		Delivery:    "fire-and-forget",
	},
	{
		Topic:       EventProviderResponse,
		Constant:    "EventProviderResponse",
		EventType:   "provider.response",
		Struct:      "ProviderResponseEvent",
		Publishers:  []string{"engine.go"},
		Subscribers: []string{"eventlogger", "sessionrecorder"},
		Scope:       ScopeInternal,
		Status:      StatusActive,
		Delivery:    "fire-and-forget",
	},
	{
		Topic:       EventSessionCreated,
		Constant:    "EventSessionCreated",
		EventType:   "session",
		Struct:      "SessionEvent",
		Publishers:  []string{"engine.go"},
		Subscribers: []string{"eventlogger", "sessionrecorder"},
		Scope:       ScopeInternal,
		Status:      StatusActive,
		Delivery:    "fire-and-forget",
		Notes: "DIVERGENCE: EventType() returns \"session\" but topic is \"session.created\"." +
			" Shared SessionEvent struct also covers session.ended;" +
			" the Action field in SessionEventData distinguishes the two." +
			" Do not change EventType() — it affects the serialised JSONL format.",
	},
	{
		Topic:       EventSessionEnded,
		Constant:    "EventSessionEnded",
		EventType:   "session",
		Struct:      "SessionEvent",
		Publishers:  []string{"engine.go"},
		Subscribers: []string{"eventlogger", "sessionrecorder"},
		Scope:       ScopeInternal,
		Status:      StatusActive,
		Delivery:    "fire-and-forget",
		Notes: "DIVERGENCE: EventType() returns \"session\" but topic is \"session.ended\"." +
			" Shares SessionEvent struct with session.created; Action field distinguishes them." +
			" Do not change EventType() — it affects the serialised JSONL format.",
	},
	{
		Topic:       EventSessionResumed,
		Constant:    "EventSessionResumed",
		EventType:   "session.resumed",
		Struct:      "SessionResumedEvent",
		Publishers:  []string{"(not yet wired — T15)"},
		Subscribers: []string{"eventlogger", "sessionrecorder"},
		Scope:       ScopeInternal,
		Status:      StatusActive,
		Delivery:    "fire-and-forget",
		Notes:       "Publisher not yet wired; T15 will add the engine publish call.",
	},
	{
		Topic:       EventToolExecuteBefore,
		Constant:    "EventToolExecuteBefore",
		EventType:   "tool",
		Struct:      "ToolEvent",
		Publishers:  []string{"engine.go"},
		Subscribers: []string{"app.go (dispatcher)", "eventlogger", "sessionrecorder"},
		Scope:       ScopeInternal,
		Status:      StatusActive,
		Delivery:    "fire-and-forget",
		Notes: "DIVERGENCE: EventType() returns \"tool\" but topic is \"tool.execute.before\"." +
			" Shares ToolEvent struct with tool.execute.after." +
			" Do not change EventType() — it affects the serialised JSONL format.",
	},
	{
		Topic:       EventToolExecuteAfter,
		Constant:    "EventToolExecuteAfter",
		EventType:   "tool",
		Struct:      "ToolEvent",
		Publishers:  []string{"engine.go"},
		Subscribers: []string{"eventlogger", "sessionrecorder"},
		Scope:       ScopeInternal,
		Status:      StatusDeprecated,
		Delivery:    "fire-and-forget",
		Notes: "DEPRECATED: Subscribers are being migrated to tool.execute.result" +
			" and tool.execute.error (T14). Engine still publishes during the transition window." +
			" DIVERGENCE: EventType() returns \"tool\" but topic is \"tool.execute.after\".",
	},
	{
		Topic:       EventToolExecuteError,
		Constant:    "EventToolExecuteError",
		EventType:   "tool.execute.error",
		Struct:      "ToolExecuteErrorEvent",
		Publishers:  []string{"engine.go"},
		Subscribers: []string{"(T14 will add subscribers)"},
		Scope:       ScopeInternal,
		Status:      StatusActive,
		Delivery:    "fire-and-forget",
		Notes:       "Replacement for tool.execute.after for error paths. Subscribers to be wired in T14.",
	},
	{
		Topic:       EventToolExecuteResult,
		Constant:    "EventToolExecuteResult",
		EventType:   "tool.execute.result",
		Struct:      "ToolExecuteResultEvent",
		Publishers:  []string{"engine.go"},
		Subscribers: []string{"(T14 will add subscribers)"},
		Scope:       ScopeInternal,
		Status:      StatusActive,
		Delivery:    "fire-and-forget",
		Notes:       "Replacement for tool.execute.after for success paths. Subscribers to be wired in T14.",
	},
	{
		Topic:       EventToolReasoning,
		Constant:    "EventToolReasoning",
		EventType:   "tool.reasoning",
		Struct:      "ToolReasoningEvent",
		Publishers:  []string{"engine.go"},
		Subscribers: []string{"eventlogger", "sessionrecorder"},
		Scope:       ScopeInternal,
		Status:      StatusActive,
		Delivery:    "fire-and-forget",
	},
}
