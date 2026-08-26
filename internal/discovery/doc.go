// Package discovery provides agent discovery using weighted token matching.
//
// This package implements intelligent agent suggestion based on user messages:
//   - Weighted token matching against agent metadata
//   - Confidence scoring for suggestions
//   - Ranking of multiple matching agents
//
// The discovery system analyses agent manifests to extract relevant keywords
// from roles, goals, and capabilities, then matches these against user input
// to suggest the most appropriate agent for each request.
package discovery
