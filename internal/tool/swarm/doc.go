// Package swarm provides agent-facing tools for inspecting registered swarm manifests.
//
// Tools are named swarm_* so manifests can grant fine-grained access:
// an agent that only needs to list swarms gets swarm_list; one that
// needs full manifest details gets swarm_info; one that validates gets
// swarm_validate.
package swarm
