package external_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("JSONRPCClient", func() {
	var (
		client *JSONRPCClient
		mockIO *mockReadWriter
	)

	BeforeEach(func() {
		mockIO = newMockReadWriter()
		client = NewJSONRPCClient(mockIO)
	})

	Describe("Call", func() {
		PIt("sends a valid JSON-RPC 2.0 request and receives a response", func() {
			params := map[string]interface{}{"foo": "bar"}
			go func() {
				// Simulate plugin response
				var req jsonrpcRequest
				json.NewDecoder(mockIO.readBuf).Decode(&req)
				resp := jsonrpcResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result:  json.RawMessage(`{"ok":true}`),
				}
				json.NewEncoder(mockIO.writeBuf).Encode(resp)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result, err := client.Call(ctx, "testMethod", params)
			Expect(err).NotTo(HaveOccurred())
			var out map[string]interface{}
			json.Unmarshal(result, &out)
			Expect(out["ok"]).To(BeTrue())
		})

		PIt("returns error on timeout", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			_, err := client.Call(ctx, "slowMethod", nil)
			Expect(err).To(MatchError(ContainSubstring("timeout")))
		})

		PIt("returns error if plugin returns JSON-RPC error", func() {
			go func() {
				var req jsonrpcRequest
				json.NewDecoder(mockIO.readBuf).Decode(&req)
				resp := jsonrpcResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   &jsonrpcError{Code: -32601, Message: "method not found"},
				}
				json.NewEncoder(mockIO.writeBuf).Encode(resp)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := client.Call(ctx, "unknownMethod", nil)
			Expect(err).To(MatchError(ContainSubstring("method not found")))
		})
	})
})

// Test helpers and stubs.
type mockReadWriter struct {
	readBuf  *io.PipeReader
	writeBuf *io.PipeWriter
}

func newMockReadWriter() *mockReadWriter {
	r, w := io.Pipe()
	return &mockReadWriter{readBuf: r, writeBuf: w}
}

func (m *mockReadWriter) Read(p []byte) (int, error)  { return m.readBuf.Read(p) }
func (m *mockReadWriter) Write(p []byte) (int, error) { return m.writeBuf.Write(p) }

// Minimal stubs for compilation.
type JSONRPCClient struct{}

func NewJSONRPCClient(rw io.ReadWriter) *JSONRPCClient { return nil }
func (c *JSONRPCClient) Call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	return nil, errors.New("not implemented")
}

type jsonrpcRequest struct{ ID int }
type jsonrpcResponse struct {
	JSONRPC string
	ID      int
	Result  json.RawMessage
	Error   *jsonrpcError
}
type jsonrpcError struct {
	Code    int
	Message string
}
