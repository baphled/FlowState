package swarmactivity

import (
	"github.com/baphled/flowstate/internal/streaming"
)

// CoalesceToolCallsForTest exposes coalesceToolCalls for external tests.
// Tests in the external _test package exercise the coalesce state machine
// directly so the logic can be verified independently of the full Render
// pipeline.
func CoalesceToolCallsForTest(
	events []streaming.SwarmEvent,
	visibleTypes map[streaming.SwarmEventType]bool,
) []string {
	return coalesceToolCalls(events, visibleTypes)
}
