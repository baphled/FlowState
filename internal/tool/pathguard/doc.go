// Package pathguard provides path-based access control for file-accessing tools.
//
// This package handles:
//   - Enforcing deny-list restrictions so agents cannot read or write sensitive directories
//   - Protecting vaults and config from file tools and bash commands
//   - Allowing MCP tools to bypass restrictions as the intended access path for protected data
package pathguard
