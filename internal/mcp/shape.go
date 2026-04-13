package mcp

import (
	"encoding/json"
	"log/slog"
	"strings"
)

// DecodeContent decodes a raw MCP tool result content string into target,
// guarding against non-JSON "empty result" responses emitted by some MCP
// servers.
//
// Some MCP servers (notably the JS reference memory server and the vault-rag
// server) return non-JSON text such as the literal string "undefined" on a
// non-error response when a query yields no results. Those responses are
// treated as an empty result, not as a decode error, so callers can surface
// "no results" semantics to consumers instead of failing the entire recall
// query.
//
// Contract:
//   - If content is empty, whitespace-only, or does not begin with '{' or
//     '[', DecodeContent returns (empty=true, err=nil) and leaves target
//     untouched. A single slog.Debug line is emitted including the supplied
//     attrs (e.g. "tool", "query_vault", "server", "vault-rag") so operators
//     can correlate silent drops with a specific caller.
//   - Otherwise, content is unmarshalled into target with encoding/json.
//     A successful decode returns (empty=false, err=nil); a failing decode
//     returns (empty=false, err=<json error>).
//
// attrs should be supplied in slog key/value pairs; they are forwarded
// verbatim to slog.Debug on the empty branch and ignored otherwise.
//
// Expected:
//   - content is a raw MCP ToolResult.Content string.
//   - target is a non-nil pointer suitable for encoding/json.Unmarshal.
//
// Returns:
//   - empty is true when content was treated as a non-JSON empty response.
//   - err is non-nil only when a JSON-shaped payload failed to decode.
//
// Side effects:
//   - Emits a debug-level log on the empty branch.
func DecodeContent(content string, target any, attrs ...any) (empty bool, err error) {
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		slog.Debug("MCP server returned non-JSON content, treating as empty result", attrs...)
		return true, nil
	}
	if err := json.Unmarshal([]byte(content), target); err != nil {
		return false, err
	}
	return false, nil
}
