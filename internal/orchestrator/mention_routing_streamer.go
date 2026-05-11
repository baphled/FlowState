package orchestrator

import (
	"context"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
)

// MentionRoutingStreamer decorates a normal Streamer with the same
// @swarm dispatch behaviour used by the chat orchestrator. It is
// intentionally narrow: ordinary agent turns pass straight through to
// base, while turns whose session is pinned to a swarm id or whose
// message contains a registered @swarm mention route through
// Orchestrator.Stream.
type MentionRoutingStreamer struct {
	base          streaming.Streamer
	orchestrator  *Orchestrator
	agentRegistry *agent.Registry
	swarmRegistry *swarm.Registry
}

// NewMentionRoutingStreamer returns a session-safe streamer wrapper
// that preserves normal agent streaming while giving persistent
// session endpoints parity with /api/chat and TUI @swarm dispatch.
func NewMentionRoutingStreamer(
	eng swarm.DispatchEngine,
	agentReg *agent.Registry,
	swarmReg *swarm.Registry,
	base streaming.Streamer,
) *MentionRoutingStreamer {
	return &MentionRoutingStreamer{
		base:          base,
		orchestrator:  New(eng, agentReg, swarmReg, base),
		agentRegistry: agentReg,
		swarmRegistry: swarmReg,
	}
}

// Stream routes only swarm-addressed session turns through the
// orchestrator. Agent turns remain byte-for-byte on the existing
// streamer path so normal chat sessions keep their current behaviour.
func (s *MentionRoutingStreamer) Stream(ctx context.Context, agentID string, message string) (<-chan provider.StreamChunk, error) {
	if s == nil || s.base == nil {
		return nil, errNoTarget
	}
	if s.shouldRoute(agentID, message) {
		return s.orchestrator.Stream(ctx, UserInput{
			Message:      message,
			DefaultAgent: agentID,
			ScanMentions: true,
		})
	}
	return s.base.Stream(ctx, agentID, message)
}

// SeedHistory forwards session history seeding to the wrapped streamer
// when it supports the optional extension. This keeps session restore
// behaviour unchanged for ordinary agent chats.
func (s *MentionRoutingStreamer) SeedHistory(sessionID string, messages []provider.Message) {
	if s == nil || s.base == nil {
		return
	}
	if seeder, ok := s.base.(streaming.HistorySeeder); ok {
		seeder.SeedHistory(sessionID, messages)
	}
}

func (s *MentionRoutingStreamer) shouldRoute(agentID string, message string) bool {
	if s.orchestrator == nil || s.swarmRegistry == nil {
		return false
	}
	if s.isSwarmID(agentID) {
		return true
	}
	return s.orchestrator.IsSwarmMention(message)
}

func (s *MentionRoutingStreamer) isSwarmID(id string) bool {
	kind, _ := swarm.Resolve(id, s.agentLookup(), s.swarmRegistry)
	return kind == swarm.KindSwarm
}

func (s *MentionRoutingStreamer) agentLookup() swarm.HasAgent {
	if s.agentRegistry == nil {
		return nil
	}
	return func(name string) bool {
		if _, ok := s.agentRegistry.Get(name); ok {
			return true
		}
		_, ok := s.agentRegistry.GetByNameOrAlias(name)
		return ok
	}
}
