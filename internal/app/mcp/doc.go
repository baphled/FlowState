// Package mcp owns MCP server wiring for the FlowState composition root.
//
// Per [[ADR - App Composition Root Boundary]] this package is the canonical
// home for MCP-server bootstrap: connection lifecycle, transport selection,
// proxy-tool materialisation, and the merge between configured and
// auto-discovered servers. The composition root (internal/app/) calls
// MergeServers + ConnectServers and orchestrates the result; this package
// does not know about the App struct, hook chain, or engine wiring.
//
// Boundary invariants (per the ADR):
//   - Imports the MCP client, the proxy tool, and config; does not import
//     internal/app/.
//   - Exports ready-to-use wiring functions and the ConnectionResult type.
//   - Existing app.MCPConnectionResult is a type alias for this package's
//     ConnectionResult so external test contracts in internal/app/ remain
//     stable while the implementation lives here.
package mcp
