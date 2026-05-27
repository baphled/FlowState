package streaming

// VerbosityLevel controls how much event detail is visible to consumers.
type VerbosityLevel int

const (
	// Minimal shows only high-level status changes, plan artefacts, and review verdicts.
	Minimal VerbosityLevel = iota
	// Standard adds tool call and delegation visibility to the minimal set.
	Standard
	// Verbose shows all events including raw text chunks and coordination store operations.
	Verbose
)

// VerbosityFilter wraps a StreamConsumer to filter typed events by verbosity level.
// Plain StreamConsumer methods (WriteChunk, WriteError, Done) pass through unconditionally
// for backward compatibility. Typed events delivered via WriteEvent are filtered by the
// configured verbosity level before forwarding to the wrapped consumer.
type VerbosityFilter struct {
	consumer StreamConsumer
	level    VerbosityLevel
}

// NewVerbosityFilter creates a VerbosityFilter wrapping the given consumer at the specified level.
//
// Expected:
//   - consumer is a non-nil StreamConsumer implementation.
//   - level is one of Minimal, Standard, or Verbose.
//
// Returns:
//   - A VerbosityFilter that implements both StreamConsumer and EventConsumer.
//
// Side effects:
//   - None.
func NewVerbosityFilter(consumer StreamConsumer, level VerbosityLevel) *VerbosityFilter {
	return &VerbosityFilter{consumer: consumer, level: level}
}

// WriteChunk passes content through to the wrapped consumer unconditionally.
//
// Expected:
//   - content contains the chunk to forward.
//
// Returns:
//   - The wrapped consumer's WriteChunk result.
//
// Side effects:
//   - Delegates the chunk to the wrapped consumer.
func (f *VerbosityFilter) WriteChunk(content string) error {
	return f.consumer.WriteChunk(content)
}

// WriteError passes the error through to the wrapped consumer.
//
// Expected:
//   - err may be nil or non-nil.
//
// Side effects:
//   - Delegates the error to the wrapped consumer.
func (f *VerbosityFilter) WriteError(err error) {
	f.consumer.WriteError(err)
}

// Done signals stream completion to the wrapped consumer.
//
// Expected:
//   - None.
//
// Side effects:
//   - Signals completion to the wrapped consumer.
func (f *VerbosityFilter) Done() {
	f.consumer.Done()
}

// WriteToolCall forwards a tool invocation to the wrapped consumer when
// the inner consumer implements ToolCallConsumer. Pre-fix the filter did
// NOT implement this optional interface; the streaming runner's type
// assertion on the filter failed, so tool calls never reached the wrapped
// /api/chat SSEConsumer or CLI WriterConsumer and chunk.Content is empty
// on tool_call chunks (the name lives on chunk.ToolCall.Name) — every
// running tool indicator was silently dropped from the chat wire.
//
// Pass-through is unconditional (no verbosity gating). Tool calls are
// Standard-level information; the SSE / CLI consumers are responsible for
// their own gating where applicable.
//
// Expected:
//   - name is the tool's identifier, optionally prefixed with "skill:".
//
// Side effects:
//   - If the wrapped consumer implements ToolCallConsumer, forwards the
//     call. Otherwise no-op.
func (f *VerbosityFilter) WriteToolCall(name string) {
	if tcc, ok := f.consumer.(ToolCallConsumer); ok {
		tcc.WriteToolCall(name)
	}
}

// WriteToolResult forwards a successful tool result to the wrapped
// consumer when the inner consumer implements ToolResultConsumer. Mirrors
// WriteToolCall's wire-loss rationale: pre-fix the chat wire dropped
// every tool_result because the filter did not relay them.
//
// Expected:
//   - content is the tool's output text.
//
// Side effects:
//   - If the wrapped consumer implements ToolResultConsumer, forwards the
//     result. Otherwise no-op.
func (f *VerbosityFilter) WriteToolResult(content string) {
	if trc, ok := f.consumer.(ToolResultConsumer); ok {
		trc.WriteToolResult(content)
	}
}

// WriteToolError forwards a failed tool execution to the wrapped consumer
// when the inner consumer implements ToolErrorConsumer. The /api/chat
// SSEConsumer emits a distinct `tool_error` wire event the frontend's
// handleToolErrorEvent flips the matching running tool_result row to
// status='error' on. Without this passthrough the typed channel never
// reaches the wire.
//
// Expected:
//   - content is the error text (typically prefixed with "Error: " for
//     the Result{Error:err} tool failure shape).
//
// Side effects:
//   - If the wrapped consumer implements ToolErrorConsumer, forwards the
//     error. Otherwise no-op.
func (f *VerbosityFilter) WriteToolError(content string) {
	if tec, ok := f.consumer.(ToolErrorConsumer); ok {
		tec.WriteToolError(content)
	}
}

// WriteEvent filters the event by verbosity level before passing to the wrapped consumer.
// Events below the configured level are silently dropped. If the wrapped consumer does not
// implement EventConsumer, the event is silently discarded after passing the level check.
//
// Expected:
//   - event is a non-nil Event implementation.
//
// Returns:
//   - nil if the event was filtered or the consumer does not support EventConsumer.
//   - The wrapped consumer's WriteEvent error if delivery was attempted.
//
// Side effects:
//   - None.
func (f *VerbosityFilter) WriteEvent(event Event) error {
	if !f.allowsEvent(event) {
		return nil
	}
	if ec, ok := f.consumer.(EventConsumer); ok {
		return ec.WriteEvent(event)
	}
	return nil
}

// allowsEvent reports whether the current verbosity level permits the event.
//
// Expected:
//   - event is a valid Event implementation.
//
// Returns:
//   - true when the event should be shown at the current level.
//   - false otherwise.
//
// Side effects:
//   - None.
func (f *VerbosityFilter) allowsEvent(event Event) bool {
	return f.level >= requiredLevel(event)
}

// requiredLevel returns the minimum verbosity level required to show the given event.
//
// Expected:
//   - event is a valid Event implementation.
//
// Returns:
//   - The minimum verbosity level required for the event.
//
// Side effects:
//   - None.
func requiredLevel(event Event) VerbosityLevel {
	switch event.(type) {
	case StatusTransitionEvent, PlanArtifactEvent, ReviewVerdictEvent:
		return Minimal
	case ToolCallEvent, DelegationEvent:
		return Standard
	case TextChunkEvent, CoordinationStoreEvent:
		return Verbose
	default:
		return Verbose
	}
}
