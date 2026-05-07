package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/discovery"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

var _ = Describe("WebSocket session handler", func() {
	var (
		mgr        *session.Manager
		srv        *api.Server
		httpServer *httptest.Server
	)

	BeforeEach(func() {
		mgr = session.NewManager(&mockStreamer{
			chunks: []provider.StreamChunk{
				{Content: "ws-response"},
				{Done: true},
			},
		})
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		srv = api.NewServer(
			&mockStreamer{chunks: []provider.StreamChunk{}},
			registry,
			disc,
			nil,
			api.WithSessionManager(mgr),
		)
		httpServer = httptest.NewServer(srv.Handler())
	})

	AfterEach(func() {
		httpServer.Close()
	})

	It("forwards messages to session engine and sends response chunks", func() {
		sess, err := mgr.CreateSession("test-agent")
		Expect(err).NotTo(HaveOccurred())

		wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/api/v1/sessions/" + sess.ID + "/ws"
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		Expect(err).NotTo(HaveOccurred())
		defer conn.CloseNow()

		msg, err := json.Marshal(map[string]string{"content": "hello"})
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Write(ctx, websocket.MessageText, msg)).To(Succeed())

		_, data, err := conn.Read(ctx)
		Expect(err).NotTo(HaveOccurred())

		var chunk map[string]interface{}
		Expect(json.Unmarshal(data, &chunk)).To(Succeed())
		Expect(chunk).To(HaveKey("content"))
		Expect(chunk["content"]).To(Equal("ws-response"))
	})

	It("returns 404 when session does not exist", func() {
		wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/api/v1/sessions/nonexistent/ws"
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		resp, err := http.Get(strings.Replace(wsURL, "ws://", "http://", 1))
		if err == nil {
			resp.Body.Close()
		}
		_, _, dialErr := websocket.Dial(ctx, wsURL, nil)
		Expect(dialErr).To(HaveOccurred())
	})
})

// Critical-stream-error severity gating regression specs (WS consumer seam).
//
// Mirrors the SSE fan-out fix landed in commit 090a2c32 across the
// WebSocket consumer at internal/api/websocket.go. Pre-fix, forwardWSChunks
// broke the loop on every chunk.Error regardless of severity, AND the
// builder seam emitted a sanitised "stream error" message that gave the
// client no way to distinguish a transient blip from a fatal provider
// failure. Both seams need to consult provider.IsCriticalStreamError so
// the WS wire mirrors the SSE wire:
//
//  1. Critical chunk.Error → emit a critical-class WS message
//     ({"error":"critical stream error", ...}) and stop streaming
//     further chunks for that turn. Subsequent buffered chunks the
//     streamer fanned out behind the failure must NOT reach the
//     client.
//
//  2. Non-critical chunk.Error → emit the existing "stream error" WS
//     message and continue streaming. Chunks after the transient
//     error must still reach the client; the consumer must NOT break
//     on every error.
//
// Both specs assert behaviour on the wire (the JSON the WS reader
// observes), not internal function names.
var _ = Describe("WebSocket session handler — critical-stream-error gating", func() {
	dialAndSend := func(httpServerURL, sessionID string) (*websocket.Conn, context.Context, context.CancelFunc) {
		wsURL := "ws" + strings.TrimPrefix(httpServerURL, "http") + "/api/v1/sessions/" + sessionID + "/ws"
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		Expect(err).NotTo(HaveOccurred())
		payload, err := json.Marshal(map[string]string{"content": "hello"})
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Write(ctx, websocket.MessageText, payload)).To(Succeed())
		return conn, ctx, cancel
	}

	readUntilClose := func(ctx context.Context, conn *websocket.Conn) []map[string]interface{} {
		var msgs []map[string]interface{}
		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				return msgs
			}
			var decoded map[string]interface{}
			if jsonErr := json.Unmarshal(raw, &decoded); jsonErr == nil {
				msgs = append(msgs, decoded)
			}
		}
	}

	It("breaks the WS chunk forward and emits a critical-class error when chunk.Error is critical, with no further chunks reaching the client", func() {
		// "401 unauthorized" matches the criticalKeywords list in
		// internal/provider/stream_error.go ("401" and "unauthori"),
		// so IsCriticalStreamError reports true. Pre-fix the
		// "after-critical-content" chunk would either reach the
		// client (if forwardWSChunks did not break on error) OR the
		// wire would carry the indistinguishable "stream error"
		// message (because the builder did not consult severity).
		// Post-fix the wire carries "critical stream error" and the
		// loop terminates before "after-critical-content" is
		// forwarded.
		mgr := session.NewManager(&mockStreamer{
			chunks: []provider.StreamChunk{
				{Content: "pre-error-content"},
				{Error: errors.New("401 unauthorized")},
				{Content: "after-critical-content"},
				{Done: true},
			},
		})
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		srv := api.NewServer(
			&mockStreamer{chunks: []provider.StreamChunk{}},
			registry,
			disc,
			nil,
			api.WithSessionManager(mgr),
		)
		httpServer := httptest.NewServer(srv.Handler())
		defer httpServer.Close()

		sess, err := mgr.CreateSession("test-agent")
		Expect(err).NotTo(HaveOccurred())

		conn, ctx, cancel := dialAndSend(httpServer.URL, sess.ID)
		defer cancel()
		defer conn.CloseNow()

		msgs := readUntilClose(ctx, conn)

		// Collect what we received on the wire so we can assert on
		// the textual signals that crossed it.
		var contents []string
		var errorMsgs []string
		for _, m := range msgs {
			if c, ok := m["content"].(string); ok && c != "" {
				contents = append(contents, c)
			}
			if e, ok := m["error"].(string); ok && e != "" {
				errorMsgs = append(errorMsgs, e)
			}
		}

		// Pre-error content must reach the client (no happy-path
		// regression before the critical chunk).
		Expect(contents).To(ContainElement("pre-error-content"),
			"chunks emitted before a critical error must still reach the client")

		// The critical signal surfaces as the typed critical-class
		// message — NOT the non-critical "stream error" text.
		Expect(errorMsgs).To(ContainElement("critical stream error"),
			"a critical chunk.Error must surface as a critical-class WS message, not as the non-critical 'stream error' message")

		// The fan-out must terminate after the critical signal.
		// Chunks the streamer produced after the critical error
		// must NOT reach the client. This is the bug the gate fixes.
		Expect(contents).NotTo(ContainElement("after-critical-content"),
			"after a critical chunk.Error the WS chunk forward must break and emit no further chunks to the client")
	})

	It("continues the WS chunk forward on a non-critical chunk.Error and lets subsequent chunks flow to the client", func() {
		// "connection refused" matches the transientKeywords list,
		// so ClassifyStreamError returns SeverityTransient and
		// IsCriticalStreamError reports false. The gate must NOT
		// fire — the consumer must continue the loop and subsequent
		// chunks must still reach the client. This pins the
		// regression-resistance contract: a future change that
		// always-breaks on chunk.Error would silently drop
		// transient-error sessions.
		mgr := session.NewManager(&mockStreamer{
			chunks: []provider.StreamChunk{
				{Content: "before-transient-error"},
				{Error: errors.New("connection refused")},
				{Content: "after-transient-error"},
				{Done: true},
			},
		})
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		srv := api.NewServer(
			&mockStreamer{chunks: []provider.StreamChunk{}},
			registry,
			disc,
			nil,
			api.WithSessionManager(mgr),
		)
		httpServer := httptest.NewServer(srv.Handler())
		defer httpServer.Close()

		sess, err := mgr.CreateSession("test-agent")
		Expect(err).NotTo(HaveOccurred())

		conn, ctx, cancel := dialAndSend(httpServer.URL, sess.ID)
		defer cancel()
		defer conn.CloseNow()

		msgs := readUntilClose(ctx, conn)

		var contents []string
		var errorMsgs []string
		for _, m := range msgs {
			if c, ok := m["content"].(string); ok && c != "" {
				contents = append(contents, c)
			}
			if e, ok := m["error"].(string); ok && e != "" {
				errorMsgs = append(errorMsgs, e)
			}
		}

		// Pre-error content reaches the client.
		Expect(contents).To(ContainElement("before-transient-error"),
			"chunks emitted before a transient error must reach the client")

		// The non-critical event surfaces with the existing
		// "stream error" message — NOT the critical-class one.
		Expect(errorMsgs).To(ContainElement("stream error"),
			"a transient chunk.Error must surface as the existing 'stream error' WS message")
		Expect(errorMsgs).NotTo(ContainElement("critical stream error"),
			"a transient chunk.Error must NOT escalate to the critical-class WS message")

		// The contract: the fan-out keeps reading and chunks after
		// a non-critical error still reach the client.
		Expect(contents).To(ContainElement("after-transient-error"),
			"after a transient chunk.Error the WS chunk forward must continue and subsequent chunks must reach the client")
	})
})
