package external_test

import (
	"context"
	"encoding/json"
	"io"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/external"
)

// pluginConn simulates bidirectional stdio between client and a mock plugin.
type pluginConn struct {
	clientR *io.PipeReader
	clientW *io.PipeWriter
	pluginR *io.PipeReader
	pluginW *io.PipeWriter
}

func newPluginConn() *pluginConn {
	pr1, pw1 := io.Pipe()
	pr2, pw2 := io.Pipe()
	return &pluginConn{clientR: pr2, clientW: pw1, pluginR: pr1, pluginW: pw2}
}

func (p *pluginConn) Read(buf []byte) (int, error)  { return p.clientR.Read(buf) }
func (p *pluginConn) Write(buf []byte) (int, error) { return p.clientW.Write(buf) }

func (p *pluginConn) Close() {
	_ = p.clientW.Close()
	_ = p.pluginW.Close()
}

var _ = Describe("JSONRPCClient", func() {
	var (
		client *external.JSONRPCClient
		conn   *pluginConn
	)

	BeforeEach(func() {
		conn = newPluginConn()
		client = external.NewJSONRPCClient(conn)
	})

	AfterEach(func() {
		conn.Close()
	})

	Describe("Call", func() {
		It("sends a valid JSON-RPC 2.0 request and receives a response", func() {
			params := map[string]interface{}{"foo": "bar"}
			go func() {
				var req struct {
					ID int64 `json:"id"`
				}
				_ = json.NewDecoder(conn.pluginR).Decode(&req)
				resp := map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]interface{}{"ok": true},
				}
				_ = json.NewEncoder(conn.pluginW).Encode(resp)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result, err := client.Call(ctx, "testMethod", params)
			Expect(err).NotTo(HaveOccurred())
			var out map[string]interface{}
			Expect(json.Unmarshal(result, &out)).To(Succeed())
			Expect(out["ok"]).To(BeTrue())
		})

		It("returns error on timeout", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			_, err := client.Call(ctx, "slowMethod", nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("timeout"))
		})

		It("returns error if plugin returns JSON-RPC error", func() {
			go func() {
				var req struct {
					ID int64 `json:"id"`
				}
				_ = json.NewDecoder(conn.pluginR).Decode(&req)
				resp := map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"error":   map[string]interface{}{"code": -32601, "message": "method not found"},
				}
				_ = json.NewEncoder(conn.pluginW).Encode(resp)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := client.Call(ctx, "unknownMethod", nil)
			Expect(err).To(MatchError(ContainSubstring("method not found")))
		})
	})
})
