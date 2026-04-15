package swarmactivity_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/streaming"
	swarmactivity "github.com/baphled/flowstate/internal/tui/components/swarm_activity"
)

// BenchmarkSwarmActivityPane_Render is a render-budget tripwire for the
// swarm activity pane. It is not a hard pass/fail test — there is no
// pinned nanosecond budget, because CI baselines drift with Go toolchain
// upgrades and terminal library changes. Instead, future engineers should
// run `go test -bench=. -benchmem ./internal/tui/components/swarm_activity/...`
// before and after changes that touch the render hot path (memoisation,
// styling, truncation, event formatting) and notice large regressions.
//
// Two scales are covered:
//   - N=200 matches DefaultSwarmStoreCapacity; this is the realistic
//     steady-state cost the chat intent pays on every frame.
//   - N=1000 exercises a post-Wave-3 world where the pane is pointed at
//     a larger persistent store. It guards against O(N^2) accidents in
//     truncation and styling.
//
// The benchmark allocates its event slice once per sub-benchmark and
// calls WithEvents + Render in the hot loop so allocations attributed to
// the render path are visible via -benchmem.
func BenchmarkSwarmActivityPane_Render(b *testing.B) {
	// Widen the viewport to 80x40 to mirror a typical dual-pane render:
	// a 120-col terminal splits 70/30, giving the secondary pane ~36
	// columns; rounding up to 80x40 ensures the pane exercises its full
	// line-count clamp at N=200 and N=1000.
	const (
		renderWidth  = 80
		renderHeight = 40
	)

	types := []streaming.SwarmEventType{
		streaming.EventDelegation,
		streaming.EventToolCall,
		streaming.EventPlan,
	}
	statuses := []string{"started", "in_progress", "completed"}
	agents := []string{
		"qa-agent",
		"senior-engineer",
		"principal-engineer",
		"tech-lead",
		"researcher",
	}

	buildEvents := func(n int) []streaming.SwarmEvent {
		events := make([]streaming.SwarmEvent, n)
		base := time.Unix(1_700_000_000, 0)
		for i := range events {
			events[i] = streaming.SwarmEvent{
				ID:        fmt.Sprintf("evt-%d", i),
				Type:      types[i%len(types)],
				Status:    statuses[i%len(statuses)],
				AgentID:   agents[i%len(agents)],
				Timestamp: base.Add(time.Duration(i) * time.Second),
			}
		}
		return events
	}

	for _, n := range []int{200, 1000} {
		events := buildEvents(n)
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			pane := swarmactivity.NewSwarmActivityPane()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_ = pane.WithEvents(events).Render(renderWidth, renderHeight)
			}
		})
	}
}
