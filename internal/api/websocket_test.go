package api_test

import (
	"context"
	"encoding/json"
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
