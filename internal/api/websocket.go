package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/baphled/flowstate/internal/dispatch"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/coder/websocket"
)

// errSessionDispatcherUnconfigured is the sentinel surfaced to the WS
// client when handleSessionWebSocket runs with a nil dispatcher. The
// auto-wire path in NewServer installs a Dispatcher whenever the
// streamer is non-nil, so production never trips this; it exists for
// test-surface compositions that opt out of the streamer entirely.
var errSessionDispatcherUnconfigured = errors.New("api: dispatcher not configured for WS session")

// wsIncomingMsg represents a message received from a WebSocket client.
type wsIncomingMsg struct {
	Content string `json:"content"`
}

// BuildWSChunkMsg converts a provider.StreamChunk to a WSChunkMsg.
//
// Expected:
//   - chunk is a valid StreamChunk.
//
// Returns:
//   - A WSChunkMsg with fields populated from the chunk.
//
// Side effects:
//   - None.
func BuildWSChunkMsg(chunk provider.StreamChunk) WSChunkMsg {
	msg := WSChunkMsg{
		Content:   chunk.Content,
		Done:      chunk.Done,
		EventType: chunk.EventType,
	}
	if chunk.Error != nil {
		// Mirror the SSE seam (handleSessionStream) by gating on
		// severity. Pre-fix every chunk.Error was sanitised as
		// "stream_error" — a fatal provider error (revoked OAuth,
		// 401, model-not-found, billing/quota lockout) reached
		// the client with the same text as a self-healing blip.
		// IsCriticalStreamError lets the wire carry a distinct
		// "critical stream error" message; the rest of the JSON
		// shape stays identical so frontends that only know about
		// the existing error+correlation_id fields keep working.
		category := "stream_error"
		if provider.IsCriticalStreamError(chunk.Error) {
			category = "stream_critical"
		}
		safeMsg, cid := clientError(chunk.Error, category)
		msg.Error = safeMsg
		msg.CorrelationID = cid
	}
	if chunk.DelegationInfo != nil {
		msg.Delegation = chunk.DelegationInfo
	}
	if chunk.ToolCall != nil {
		msg.ToolCall = chunk.ToolCall
	}
	if chunk.Event != nil {
		if progressEvent, ok := chunk.Event.(streaming.ProgressEvent); ok {
			msg.Progress = &progressEvent
		}
		if _, ok := chunk.Event.(streaming.Event); ok {
			msg.EventData = chunk.Event
		}
	}
	return msg
}

// WSChunkMsg represents a response chunk sent to a WebSocket client.
type WSChunkMsg struct {
	Content       string                   `json:"content,omitempty"`
	Done          bool                     `json:"done,omitempty"`
	Error         string                   `json:"error,omitempty"`
	CorrelationID string                   `json:"correlation_id,omitempty"`
	Delegation    *provider.DelegationInfo `json:"delegation,omitempty"`
	ToolCall      *provider.ToolCall       `json:"tool_call,omitempty"`
	Progress      *streaming.ProgressEvent `json:"progress,omitempty"`
	EventType     string                   `json:"event_type,omitempty"`
	EventData     interface{}              `json:"event_data,omitempty"`
}

// originAllowlist returns the configured WebSocket / HTTP Origin allowlist,
// defaulting to ["localhost:*"] when none was provided via WithOriginPatterns.
// This preserves the pre-PR1 behaviour (literal "localhost:*" at websocket.go:106)
// while letting deployers override via cfg.Auth.AllowedOrigins. The same list
// feeds PR3's internal/auth.RequireOrigin HTTP middleware so the WebSocket and
// HTTP surfaces never drift.
func (s *Server) originAllowlist() []string {
	if len(s.originPatterns) == 0 {
		return []string{"localhost:*"}
	}
	return s.originPatterns
}

// handleSessionWebSocket upgrades the connection to WebSocket, validates the session,
// then forwards incoming messages to the session engine and streams engine responses back.
//
// Expected:
//   - Request path parameter "id" identifies an existing session.
//   - The request can be upgraded to a WebSocket connection.
//
// Side effects:
//   - Reads JSON messages from the client and forwards them to the session engine.
//   - Writes engine response chunks as JSON to the client.
//   - Subscribes to EventBus events for the session and forwards them to the client.
//   - Closes the connection when the engine stream is complete or an error occurs.
func (s *Server) handleSessionWebSocket(w http.ResponseWriter, r *http.Request) {
	if s.sessionManager == nil {
		http.Error(w, errSessionManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	id := r.PathValue("id")
	if _, err := s.sessionManager.GetSession(id); err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: s.originAllowlist(),
	})
	if err != nil {
		return
	}

	out := make(chan WSChunkMsg, 128)
	quit := make(chan struct{})
	writeDone := make(chan struct{})
	// Decouple the WS read/write lifecycle from the request context so
	// the underlying conn.Read does not park on a cancelled ctx after a
	// hijacked WS connection's request context is cancelled by the
	// http.Server's connection-state tracker. The Accept doc explicitly
	// warns "using the http.Request Context after Accept returns may
	// lead to unexpected behavior (see http.Hijacker)" — so the read
	// and write loops use a handler-owned context tied to the conn
	// lifecycle instead. The engine's lifetime is decoupled from BOTH
	// this handler-owned ctx and the request ctx by
	// Dispatcher.DispatchSessioned (context.WithoutCancel at the seam),
	// closing S1 of the v6 Dispatcher Service Unification plan.
	rwCtx, rwCancel := context.WithCancel(context.Background())
	defer rwCancel()

	go func() {
		writeWSLoop(rwCtx, conn, out, quit)
		close(writeDone)
	}()

	stopBus := s.subscribeSessionBus(id, out)

	for {
		incoming, ok := readWSMessage(rwCtx, conn)
		if !ok {
			break
		}
		if !s.serveWSSession(rwCtx, out, quit, id, incoming) {
			break
		}
	}

	// C2 — channel-close ordering. Pre-fix this did `close(out)` then
	// `stopBus()`. Bus subscribe handlers (event_bridge.go) use
	// `select { case out <- msg: default: }` — on a CLOSED channel
	// `select` does NOT take the default branch, it panics with
	// "send on closed channel". Between `close(out)` and Unsubscribe
	// any of nine bus topics firing (tool.before/result/error,
	// rate_limited, three background_task variants, context_compacted,
	// gate.failed) crashes the publisher's goroutine.
	//
	// Fix: never close `out`. Mirrors the SSE handler pattern in
	// server.go (handleSessionStream / busCh): the channel is left open
	// and goes out of scope when the request handler returns; bus
	// handlers held in stale Publish-snapshots either deliver into the
	// buffer or take the default branch on a full chan, never panic.
	// We signal the writer via `quit` and drive shutdown as:
	//   1. stopBus() — Unsubscribe removes our handlers; future
	//      Publish snapshots will not include them.
	//   2. close(quit) — writer's select sees quit closed and exits.
	//   3. <-writeDone — wait for the writer goroutine to drain.
	//   4. closeWebSocket(conn) — tear down the underlying connection.
	// eventbus.Publish snapshots handlers under RLock and dispatches
	// outside the lock, so a publish that started before stopBus may
	// still call a now-removed handler — that handler now writes to an
	// open buffered channel (16 slots in the SSE path, 128 here) or
	// drops via the default branch when the buffer is full. Neither
	// path panics, which is the C2 fix invariant.
	stopBus()
	close(quit)
	<-writeDone
	closeWebSocket(conn)
}

// readWSMessage reads and decodes the next message from the WebSocket connection.
//
// Expected:
//   - conn is an open WebSocket connection.
//
// Returns:
//   - The decoded message and true on success.
//   - An empty message and false when the connection closes or the message is empty.
//
// Side effects:
//   - Blocks until a message is available.
func readWSMessage(ctx context.Context, conn *websocket.Conn) (wsIncomingMsg, bool) {
	_, raw, err := conn.Read(ctx)
	if err != nil {
		return wsIncomingMsg{}, false
	}
	var msg wsIncomingMsg
	if jsonErr := json.Unmarshal(raw, &msg); jsonErr != nil || msg.Content == "" {
		return wsIncomingMsg{}, true
	}
	return msg, true
}

// writeWSLoop reads messages from the out channel and writes each to the WebSocket connection.
// This ensures all writes are serialised through a single goroutine, preventing concurrent writes.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - conn is an open WebSocket connection.
//   - out is a readable channel of WSChunkMsg values.
//   - quit is closed by the handler when the connection should drain and exit.
//
// Side effects:
//   - Writes JSON-encoded messages to conn until quit is closed, ctx is
//     cancelled, or sendWSMsg returns an error.
//
// Note (Bug C2): the loop intentionally does not range over out — out is
// never closed by the handler (handlers take a default branch on a full
// buffer rather than panic on a closed chan). Shutdown is signalled via
// quit. After quit is closed the writer drains any buffered messages so
// chunks already in flight reach the client before the connection tears
// down, then exits.
func writeWSLoop(ctx context.Context, conn *websocket.Conn, out <-chan WSChunkMsg, quit <-chan struct{}) {
	for {
		select {
		case msg := <-out:
			if err := sendWSMsg(ctx, conn, msg); err != nil {
				return
			}
		case <-quit:
			// Drain any buffered messages so in-flight chunks
			// reach the client before tearing down.
			for {
				select {
				case msg := <-out:
					if err := sendWSMsg(ctx, conn, msg); err != nil {
						return
					}
				default:
					return
				}
			}
		}
	}
}

// serveWSSession forwards an incoming message through
// dispatch.Dispatcher.DispatchSessioned (Phase 4 of the v6 Dispatcher
// Service Unification plan) and lets the dispatcher's consumer fan-out
// stream chunks through the WSStreamConsumer wired over `out` + `quit`.
//
// The pre-Phase-4 path called the session manager's SendMessage
// directly with the request ctx, which coupled the engine's lifetime
// to the handler's ctx
// (originally r.Context()). On a hijacked WS connection the http.Server
// may cancel r.Context() unpredictably; before Phase 4 that
// cancellation cascaded into the engine and emitted `{Error: ctx.Err(),
// Done: true}` with truncated content. Routing through DispatchSessioned
// applies context.WithoutCancel at the seam so the engine outlives both
// the WS request context AND the handler's own rwCtx, closing S1 of
// the v6 plan and mirroring commit 51fb416c's POST /messages fix.
//
// The session manager already appends the user message synchronously
// inside DispatchSessioned (see Dispatcher.DispatchSessioned step 1).
// The SessionedHandle.Snapshot return value is unused for WS — the
// frame writer streams chunks via the consumer, not a JSON snapshot.
//
// AgentID resolution mirrors handleSessionMessage:
// CurrentAgentID || AgentID. Dispatcher uses this as the fallback when
// no in-content @-mention resolves to a swarm.
//
// Expected:
//   - ctx is the handler's read/write context.
//   - out is the WSChunkMsg fan-out channel drained by writeWSLoop.
//   - quit is closed by the handler when the connection should drain
//     and shutdown; the consumer honours quit so the dispatcher's fan-
//     out goroutine does not park on a dead writer.
//   - sessionID identifies an existing session.
//   - msg contains the content to send to the engine.
//
// Returns:
//   - true to continue the read loop, false to close the connection.
//
// Side effects:
//   - Calls Dispatcher.DispatchSessioned which appends the user message
//     and spawns the streamer goroutine.
//   - Sends WSChunkMsg values through `out` via the consumer.
func (s *Server) serveWSSession(ctx context.Context, out chan<- WSChunkMsg, quit <-chan struct{}, sessionID string, msg wsIncomingMsg) bool {
	if msg.Content == "" {
		return true
	}
	if s.completionOrchestrator != nil {
		s.completionOrchestrator.ResetRePromptCount(sessionID)
	}

	if s.dispatcher == nil {
		// Dispatcher not configured — surface as a transient error
		// frame so the client can render a soft-error and try again.
		// The auto-wire in NewServer installs a Dispatcher whenever the
		// streamer is non-nil; this path is reachable only in tests
		// that opt out of auto-wire by passing WithDispatcher(nil) (no
		// such call exists today) or omit the streamer entirely.
		safeMsg, cid := clientError(errSessionDispatcherUnconfigured, "stream_error")
		select {
		case out <- WSChunkMsg{Error: safeMsg, CorrelationID: cid}:
		case <-quit:
		}
		return false
	}

	// AgentID fallback mirrors handleSessionMessage's resolve. Snapshot
	// captures CurrentAgentID || AgentID under RLock so Dispatcher's
	// in-content mention scan + AutoDispatchSwarmFor fallback picks the
	// session's persistent target when no @-mention hits.
	snap, snapErr := s.sessionManager.SnapshotSession(sessionID)
	if snapErr != nil {
		// Session vanished between Accept and the first message —
		// surface a transient error and close the WS turn.
		safeMsg, cid := clientError(snapErr, "session_not_found")
		select {
		case out <- WSChunkMsg{Error: safeMsg, CorrelationID: cid}:
		case <-quit:
		}
		return false
	}
	agentID := snap.AgentID
	if snap.CurrentAgentID != "" {
		agentID = snap.CurrentAgentID
	}

	consumer := NewWSStreamConsumer(out, quit)
	if _, dispatchErr := s.dispatcher.DispatchSessioned(ctx, dispatch.DispatchRequest{
		SessionID:    sessionID,
		AgentID:      agentID,
		Content:      msg.Content,
		ScanMentions: true,
	}, consumer); dispatchErr != nil {
		safeMsg, cid := clientError(dispatchErr, "stream_error")
		select {
		case out <- WSChunkMsg{Error: safeMsg, CorrelationID: cid}:
		case <-quit:
		}
		return false
	}
	// Dispatcher returned synchronously after appending the user
	// message; the streamer runs in a Dispatcher-owned goroutine and
	// streams through the consumer. Continue the read loop so multi-
	// turn sessions over a single WS connection keep flowing.
	return true
}

// sendWSMsg encodes msg as JSON and writes it to the WebSocket connection.
//
// Expected:
//   - conn is an open WebSocket connection.
//   - msg is JSON-serialisable.
//
// Returns:
//   - An error if marshalling or the write fails.
//
// Side effects:
//   - Writes a JSON text frame to the WebSocket connection.
func sendWSMsg(ctx context.Context, conn *websocket.Conn, msg WSChunkMsg) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

// closeWebSocket closes the WebSocket connection if possible.
//
// Expected:
//   - The connection can be closed without panicking.
//
// Side effects:
//   - Closes the underlying WebSocket connection.
func closeWebSocket(conn *websocket.Conn) {
	if err := conn.CloseNow(); err != nil {
		return
	}
}

// WSStreamConsumer is the chunk-fan-out shim wired through
// dispatch.Dispatcher.DispatchSessioned by handleSessionWebSocket
// (Phase 4 of the v6 Dispatcher Service Unification plan). It implements
// streaming.StreamConsumer plus the optional consumer interfaces
// (DelegationConsumer, ProgressConsumer, EventConsumer,
// HarnessEventConsumer, ToolCallConsumer, ToolResultConsumer) by
// mapping each signal onto a WSChunkMsg and pushing it through the
// handler's existing `out` channel. The handler's writeWSLoop drains
// `out` exactly as it did pre-Phase-4; the only thing that changes is
// the producer — chunks now arrive via the Dispatcher's streaming.Run-
// shaped fan-out (internal/streaming/runner.go) rather than directly
// from sessionManager.SendMessage.
//
// quit is the same signal writeWSLoop watches for shutdown; once quit
// closes, the consumer takes the default branch on subsequent sends so
// a slow-or-dead writer does not park the dispatcher's fan-out
// goroutine indefinitely. The `out` channel is left open (Bug C2's
// load-bearing invariant — see writeWSLoop's godoc and the cleanup
// comment at handleSessionWebSocket:147-176): the consumer never
// closes it.
type WSStreamConsumer struct {
	out  chan<- WSChunkMsg
	quit <-chan struct{}
	// criticalSeen latches once a critical chunk.Error has surfaced.
	// Subsequent WriteChunk / WriteDelegation / WriteProgress / etc.
	// calls become no-ops so chunks the streamer fanned out behind the
	// failure do not reach the WS client. This mirrors the pre-Phase-4
	// forwardWSChunks severity gate (websocket.go:295-307 pre-deletion)
	// which broke the loop on critical errors but continued on
	// transient ones. The Done frame is still emitted so the WS
	// connection settles cleanly.
	//
	// Single-goroutine access: the Dispatcher's fan-out goroutine
	// (internal/dispatch/dispatcher.go::driveConsumer) drives every
	// Write call serially through one consumer instance per WS turn,
	// so a non-atomic bool is sufficient.
	criticalSeen bool
}

// NewWSStreamConsumer wires a WSStreamConsumer over the WS handler's
// out channel and quit signal. The returned consumer is safe to pass to
// dispatch.Dispatcher.DispatchSessioned; chunks are mapped onto
// WSChunkMsg via the same helpers BuildWSChunkMsg uses (severity-gated
// error category, harness event types, delegation / progress / event
// fan-out).
//
// Expected:
//   - out is the WS handler's existing buffered WSChunkMsg channel.
//   - quit is closed by the handler when the connection should drain
//     and shutdown.
//
// Returns:
//   - A configured *WSStreamConsumer.
//
// Side effects:
//   - None.
func NewWSStreamConsumer(out chan<- WSChunkMsg, quit <-chan struct{}) *WSStreamConsumer {
	return &WSStreamConsumer{out: out, quit: quit}
}

// send pushes a WSChunkMsg into the out channel honouring the quit
// signal. The default branch on a closed `quit` keeps the dispatcher's
// fan-out goroutine from parking when the WS connection has already
// torn down. Pre-Phase-4 the same backpressure surface lived in
// forwardWSChunks (websocket.go:285-291) — the shape is preserved
// here.
//
// Side effects:
//   - Sends msg to out when capacity permits; drops msg when quit is
//     closed and the writer is no longer draining.
func (c *WSStreamConsumer) send(msg WSChunkMsg) {
	select {
	case c.out <- msg:
	case <-c.quit:
		return
	}
}

// sendUnlessCritical is the post-critical-gate variant of send. Most
// non-content signals (delegation, progress, tool, harness, typed
// events) follow the same severity contract as content chunks — once a
// fatal provider error has surfaced the wire should not continue
// emitting structured side-events for the failed turn. Error and Done
// are the two exceptions (they have their own emit paths).
//
// Side effects:
//   - Sends msg only when criticalSeen is false.
func (c *WSStreamConsumer) sendUnlessCritical(msg WSChunkMsg) {
	if c.criticalSeen {
		return
	}
	c.send(msg)
}

// WriteChunk delivers a content fragment to the WS frame writer. After
// a critical chunk.Error has surfaced (criticalSeen latched), subsequent
// content frames are dropped so the WS client never sees chunks the
// streamer fanned out behind a fatal failure — mirrors the pre-Phase-4
// forwardWSChunks severity gate.
//
// Side effects:
//   - Sends a WSChunkMsg{Content} through out (or drops on quit /
//     post-critical).
func (c *WSStreamConsumer) WriteChunk(content string) error {
	if c.criticalSeen {
		return nil
	}
	c.send(WSChunkMsg{Content: content})
	return nil
}

// WriteError surfaces a stream-level error as a WSChunkMsg. The error
// is sanitised through clientError (which redacts internal details to a
// stable category + correlation id), and the category is gated by
// provider.IsCriticalStreamError so the wire continues to distinguish
// transient blips from fatal provider failures — same shape as
// BuildWSChunkMsg's pre-Phase-4 error path. A critical error latches
// the consumer's criticalSeen flag so subsequent WriteChunk calls drop
// silently; transient errors do not latch and the stream continues.
//
// Side effects:
//   - Sends a WSChunkMsg with Error + CorrelationID through out.
//   - Latches criticalSeen when the error matches IsCriticalStreamError.
func (c *WSStreamConsumer) WriteError(err error) {
	if err == nil {
		return
	}
	category := "stream_error"
	critical := provider.IsCriticalStreamError(err)
	if critical {
		category = "stream_critical"
	}
	safeMsg, cid := clientError(err, category)
	c.send(WSChunkMsg{Error: safeMsg, CorrelationID: cid})
	if critical {
		c.criticalSeen = true
	}
}

// Done signals the terminal chunk to the WS client by emitting a
// WSChunkMsg{Done: true}. The pre-Phase-4 path emitted the same shape
// via BuildWSChunkMsg when chunk.Done was true. Done is emitted even
// after criticalSeen latches so the WS frame writer settles into a
// known-terminal state.
//
// Side effects:
//   - Sends a WSChunkMsg{Done: true} through out.
func (c *WSStreamConsumer) Done() {
	c.send(WSChunkMsg{Done: true})
}

// WriteDelegation surfaces a delegation status event as a WSChunkMsg.
// Mirrors WSConsumer.WriteDelegationToMsg's mapping (same DelegationInfo
// fields stamped). The WS handler's pre-Phase-4 path got this shape via
// BuildWSChunkMsg's chunk.DelegationInfo branch.
//
// Returns:
//   - Always nil (the channel send is best-effort under quit).
//
// Side effects:
//   - Sends a WSChunkMsg with Delegation through out.
func (c *WSStreamConsumer) WriteDelegation(ev streaming.DelegationEvent) error {
	c.sendUnlessCritical(WSChunkMsg{Delegation: &provider.DelegationInfo{
		SourceAgent:  ev.SourceAgent,
		TargetAgent:  ev.TargetAgent,
		ChainID:      ev.ChainID,
		Status:       ev.Status,
		ModelName:    ev.ModelName,
		ProviderName: ev.ProviderName,
		Description:  ev.Description,
		ToolCalls:    ev.ToolCalls,
		LastTool:     ev.LastTool,
		StartedAt:    ev.StartedAt,
		CompletedAt:  ev.CompletedAt,
	}})
	return nil
}

// WriteProgress surfaces a delegation progress event as a WSChunkMsg.
// Mirrors WSConsumer.WriteProgressToMsg's mapping.
//
// Returns:
//   - Always nil.
//
// Side effects:
//   - Sends a WSChunkMsg with Progress through out.
func (c *WSStreamConsumer) WriteProgress(ev streaming.ProgressEvent) error {
	c.sendUnlessCritical(WSChunkMsg{Progress: &ev})
	return nil
}

// WriteEvent surfaces a typed streaming Event as a WSChunkMsg. Mirrors
// WSConsumer.WriteEventToMsg's mapping (event_type + event_data fields).
//
// Returns:
//   - Always nil.
//
// Side effects:
//   - Sends a WSChunkMsg with EventType + EventData through out.
func (c *WSStreamConsumer) WriteEvent(event streaming.Event) error {
	c.sendUnlessCritical(WSChunkMsg{
		EventType: event.Type(),
		EventData: event,
	})
	return nil
}

// WriteHarnessRetry surfaces a harness retry event as a WSChunkMsg.
// Mirrors WSConsumer.WriteHarnessRetryToMsg's mapping.
//
// Side effects:
//   - Sends a WSChunkMsg with EventType=harness_retry + Content.
func (c *WSStreamConsumer) WriteHarnessRetry(content string) {
	c.sendUnlessCritical(WSChunkMsg{EventType: "harness_retry", Content: content})
}

// WriteAttemptStart surfaces a harness attempt start event as a
// WSChunkMsg.
//
// Side effects:
//   - Sends a WSChunkMsg with EventType=harness_attempt_start + Content.
func (c *WSStreamConsumer) WriteAttemptStart(content string) {
	c.sendUnlessCritical(WSChunkMsg{EventType: "harness_attempt_start", Content: content})
}

// WriteComplete surfaces a harness completion event as a WSChunkMsg.
//
// Side effects:
//   - Sends a WSChunkMsg with EventType=harness_complete + Content.
func (c *WSStreamConsumer) WriteComplete(content string) {
	c.sendUnlessCritical(WSChunkMsg{EventType: "harness_complete", Content: content})
}

// WriteCriticFeedback surfaces a harness critic feedback event as a
// WSChunkMsg.
//
// Side effects:
//   - Sends a WSChunkMsg with EventType=harness_critic_feedback + Content.
func (c *WSStreamConsumer) WriteCriticFeedback(content string) {
	c.sendUnlessCritical(WSChunkMsg{EventType: "harness_critic_feedback", Content: content})
}

// WriteToolCall surfaces a tool invocation as a WSChunkMsg. Mirrors
// BuildWSChunkMsg's chunk.ToolCall branch (which expects the caller to
// have stamped the *provider.ToolCall directly on the chunk).
//
// Side effects:
//   - Sends a WSChunkMsg with ToolCall through out.
func (c *WSStreamConsumer) WriteToolCall(name string) {
	c.sendUnlessCritical(WSChunkMsg{ToolCall: &provider.ToolCall{Name: name}})
}

// WriteToolResult surfaces a tool result as a WSChunkMsg's Content
// field. The pre-Phase-4 chunk.ToolResult path did not have a dedicated
// JSON field on WSChunkMsg — the result content fed back into the
// stream as a regular content chunk via the engine's tool_result loop.
// Preserve that shape so frontends that only watch for content keep
// working.
//
// Side effects:
//   - Sends a WSChunkMsg with Content through out.
func (c *WSStreamConsumer) WriteToolResult(content string) {
	c.sendUnlessCritical(WSChunkMsg{Content: content})
}

// WSConsumer implements streaming.DelegationConsumer, streaming.DelegationProgressConsumer,
// streaming.EventConsumer, and streaming.HarnessEventConsumer by forwarding events as
// JSON over a WebSocket connection.
type WSConsumer struct {
	conn *websocket.Conn
	ctx  context.Context
}

// NewWSConsumer creates a WSConsumer for sending events over a WebSocket connection.
//
// Expected:
//   - ctx is a valid context for the WebSocket operations.
//   - conn is an open WebSocket connection.
//
// Returns:
//   - A configured WSConsumer.
//
// Side effects:
//   - None.
func NewWSConsumer(ctx context.Context, conn *websocket.Conn) *WSConsumer {
	return &WSConsumer{conn: conn, ctx: ctx}
}

// WriteDelegation sends a DelegationEvent as a WebSocket message to the client.
//
// Expected:
//   - ev is a valid DelegationEvent.
//   - conn is an open WebSocket connection.
//
// Returns:
//   - An error if the write fails.
//
// Side effects:
//   - Writes JSON to the WebSocket connection.
func (c *WSConsumer) WriteDelegation(ev streaming.DelegationEvent) error {
	msg := c.WriteDelegationToMsg(ev)
	return sendWSMsg(c.ctx, c.conn, msg)
}

// WriteDelegationToMsg converts a DelegationEvent to a WSChunkMsg for testing.
//
// Expected:
//   - ev is a valid DelegationEvent.
//
// Returns:
//   - A WSChunkMsg with the delegation field populated.
//
// Side effects:
//   - None.
func (c *WSConsumer) WriteDelegationToMsg(ev streaming.DelegationEvent) WSChunkMsg {
	return WSChunkMsg{
		Delegation: &provider.DelegationInfo{
			SourceAgent:  ev.SourceAgent,
			TargetAgent:  ev.TargetAgent,
			ChainID:      ev.ChainID,
			Status:       ev.Status,
			ModelName:    ev.ModelName,
			ProviderName: ev.ProviderName,
			Description:  ev.Description,
			ToolCalls:    ev.ToolCalls,
			LastTool:     ev.LastTool,
			StartedAt:    ev.StartedAt,
			CompletedAt:  ev.CompletedAt,
		},
	}
}

// WriteProgress sends a ProgressEvent as a WebSocket message to the client.
//
// Expected:
//   - ev is a valid ProgressEvent.
//   - conn is an open WebSocket connection.
//
// Returns:
//   - An error if the write fails.
//
// Side effects:
//   - Writes JSON to the WebSocket connection.
func (c *WSConsumer) WriteProgress(ev streaming.ProgressEvent) error {
	msg := c.WriteProgressToMsg(ev)
	return sendWSMsg(c.ctx, c.conn, msg)
}

// WriteProgressToMsg converts a ProgressEvent to a WSChunkMsg for testing.
//
// Expected:
//   - ev is a valid ProgressEvent.
//
// Returns:
//   - A WSChunkMsg with the progress field populated.
//
// Side effects:
//   - None.
func (c *WSConsumer) WriteProgressToMsg(ev streaming.ProgressEvent) WSChunkMsg {
	return WSChunkMsg{
		Progress: &ev,
	}
}

// WriteEvent sends a typed streaming Event as a WebSocket message to the client.
//
// Expected:
//   - event is a non-nil Event implementation.
//   - conn is an open WebSocket connection.
//
// Returns:
//   - An error if the write fails.
//
// Side effects:
//   - Writes JSON to the WebSocket connection.
func (c *WSConsumer) WriteEvent(event streaming.Event) error {
	msg := c.WriteEventToMsg(event)
	return sendWSMsg(c.ctx, c.conn, msg)
}

// WriteEventToMsg converts a typed streaming Event to a WSChunkMsg for testing.
//
// Expected:
//   - event is a non-nil Event implementation.
//
// Returns:
//   - A WSChunkMsg with event_type and event_data fields populated.
//
// Side effects:
//   - None.
func (c *WSConsumer) WriteEventToMsg(event streaming.Event) WSChunkMsg {
	return WSChunkMsg{
		EventType: event.Type(),
		EventData: event,
	}
}

// WriteHarnessRetry sends a harness retry event as a WebSocket message to the client.
//
// Expected:
//   - content describes the validation failure and retry reason.
//   - conn is an open WebSocket connection.
//
// Side effects:
//   - Writes JSON to the WebSocket connection.
func (c *WSConsumer) WriteHarnessRetry(content string) {
	msg := c.WriteHarnessRetryToMsg(content)
	c.sendHarnessMsg(msg)
}

// WriteHarnessRetryToMsg converts harness retry content to a WSChunkMsg for testing.
//
// Expected:
//   - content describes the validation failure and retry reason.
//
// Returns:
//   - A WSChunkMsg with event_type set to harness_retry and content populated.
//
// Side effects:
//   - None.
func (c *WSConsumer) WriteHarnessRetryToMsg(content string) WSChunkMsg {
	return WSChunkMsg{
		EventType: "harness_retry",
		Content:   content,
	}
}

// WriteAttemptStart sends a harness attempt start event as a WebSocket message to the client.
//
// Expected:
//   - content describes the attempt being started.
//   - conn is an open WebSocket connection.
//
// Side effects:
//   - Writes JSON to the WebSocket connection.
func (c *WSConsumer) WriteAttemptStart(content string) {
	msg := c.WriteAttemptStartToMsg(content)
	c.sendHarnessMsg(msg)
}

// WriteAttemptStartToMsg converts harness attempt start content to a WSChunkMsg for testing.
//
// Expected:
//   - content describes the attempt being started.
//
// Returns:
//   - A WSChunkMsg with event_type set to harness_attempt_start and content populated.
//
// Side effects:
//   - None.
func (c *WSConsumer) WriteAttemptStartToMsg(content string) WSChunkMsg {
	return WSChunkMsg{
		EventType: "harness_attempt_start",
		Content:   content,
	}
}

// WriteComplete sends a harness completion event as a WebSocket message to the client.
//
// Expected:
//   - content describes the evaluation outcome.
//   - conn is an open WebSocket connection.
//
// Side effects:
//   - Writes JSON to the WebSocket connection.
func (c *WSConsumer) WriteComplete(content string) {
	msg := c.WriteCompleteToMsg(content)
	c.sendHarnessMsg(msg)
}

// WriteCompleteToMsg converts harness completion content to a WSChunkMsg for testing.
//
// Expected:
//   - content describes the evaluation outcome.
//
// Returns:
//   - A WSChunkMsg with event_type set to harness_complete and content populated.
//
// Side effects:
//   - None.
func (c *WSConsumer) WriteCompleteToMsg(content string) WSChunkMsg {
	return WSChunkMsg{
		EventType: "harness_complete",
		Content:   content,
	}
}

// WriteCriticFeedback sends a harness critic feedback event as a WebSocket message to the client.
//
// Expected:
//   - content describes the critic's feedback on the plan.
//   - conn is an open WebSocket connection.
//
// Side effects:
//   - Writes JSON to the WebSocket connection.
func (c *WSConsumer) WriteCriticFeedback(content string) {
	msg := c.WriteCriticFeedbackToMsg(content)
	c.sendHarnessMsg(msg)
}

// WriteCriticFeedbackToMsg converts harness critic feedback to a WSChunkMsg for testing.
//
// Expected:
//   - content describes the critic's feedback on the plan.
//
// Returns:
//   - A WSChunkMsg with event_type set to harness_critic_feedback and content populated.
//
// Side effects:
//   - None.
func (c *WSConsumer) WriteCriticFeedbackToMsg(content string) WSChunkMsg {
	return WSChunkMsg{
		EventType: "harness_critic_feedback",
		Content:   content,
	}
}

// sendHarnessMsg sends a harness event message over the WebSocket, discarding errors
// because the HarnessEventConsumer interface methods do not return errors.
//
// Expected:
//   - msg is a valid WSChunkMsg to send.
//   - c.conn is an open WebSocket connection.
//
// Side effects:
//   - Writes JSON to the WebSocket connection if the connection is available.
func (c *WSConsumer) sendHarnessMsg(msg WSChunkMsg) {
	if err := sendWSMsg(c.ctx, c.conn, msg); err != nil {
		return
	}
}
