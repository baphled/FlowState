// Package swarm loads, validates and registers FlowState swarm
// manifests. A swarm manifest is the user-facing veneer over the
// agent-platform's swarm runtime: it names a lead, an explicit member
// roster, the harness configuration that wraps the run, and any
// swarm-level gates.
//
// The package mirrors the structure of internal/agent: a Manifest
// type with a Validate method, a Registry keyed by id, a directory
// loader (Load / LoadDir / NewRegistryFromDir), and a small Validator
// interface used by the agent + swarm registry-aware checks. The
// agent registry dependency is taken via the AgentRegistry interface
// to keep this package free of an internal/agent import.
//
// See the "Swarm Manifests and Lead Invocation Addendum (April 2026)"
// plan §1 for the schema reference and the validation rules
// implemented here.
package swarm
