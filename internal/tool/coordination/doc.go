// Package coordination provides a tool for accessing the coordination store
// from agent contexts.
//
// This package implements the tool.Tool interface to expose coordination store
// operations (get, set, list) to agents during delegation chains. The tool
// allows agents to read and write shared context data including requirements,
// interview transcripts, codebase findings, external references, analysis,
// plans, and review verdicts.
package coordination
