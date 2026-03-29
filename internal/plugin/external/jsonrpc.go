package external

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// jsonrpcRequest is a JSON-RPC 2.0 request message.
type jsonrpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// jsonrpcResponse is a JSON-RPC 2.0 response message.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

// jsonrpcError is the error object in a JSON-RPC 2.0 error response.
type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// JSONRPCClient communicates with an external plugin process over JSON-RPC 2.0.
type JSONRPCClient struct {
	mu  sync.Mutex
	enc *json.Encoder
	dec *json.Decoder
	seq atomic.Int64
}

// NewJSONRPCClient returns a JSONRPCClient backed by the given read/write connection.
func NewJSONRPCClient(conn io.ReadWriter) *JSONRPCClient {
	return &JSONRPCClient{
		enc: json.NewEncoder(conn),
		dec: json.NewDecoder(conn),
	}
}

// Call sends a JSON-RPC 2.0 request and returns the result.
func (c *JSONRPCClient) Call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := c.seq.Add(1)
	req := jsonrpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}

	type callResult struct {
		data json.RawMessage
		err  error
	}
	ch := make(chan callResult, 1)

	go func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if encErr := c.enc.Encode(req); encErr != nil {
			ch <- callResult{err: fmt.Errorf("encode request: %w", encErr)}
			return
		}
		var resp jsonrpcResponse
		if decErr := c.dec.Decode(&resp); decErr != nil {
			ch <- callResult{err: fmt.Errorf("decode response: %w", decErr)}
			return
		}
		if resp.Error != nil {
			ch <- callResult{err: fmt.Errorf("%s (code %d)", resp.Error.Message, resp.Error.Code)}
			return
		}
		ch <- callResult{data: resp.Result}
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for %q response: %w", method, ctx.Err())
	case r := <-ch:
		return r.data, r.err
	}
}
