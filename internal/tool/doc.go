// Package tool defines the tool interface and registry for agent capabilities.
//
// This package provides:
//   - Common interface for tools that agents can invoke
//   - Tool registry for managing available tools
//   - Permission checking for tool access control
//   - Input validation and schema definitions
//
// Concrete tool implementations (bash, file, web) are in sub-packages
// and implement the Tool interface defined here.
package tool
