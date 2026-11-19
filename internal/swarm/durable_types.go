package swarm

import (
	"context"

	"github.com/baphled/flowstate/internal/store"
)

// RunSink is the persistence port of durable run receipts;
// internal/store's Store satisfies it with RecordSwarmRun.
type RunSink interface {
	// RecordSwarmRun persists one swarm run and its packet rows in a
	// single transaction.
	RecordSwarmRun(ctx context.Context, run store.SwarmRun) (store.SwarmRun, error)
}

// DurableReceipts is the RunReceipts implementation that makes a run
// durable: it buffers the run loop's admission decisions and flushes
// one complete swarm run through the store when the gate decision is
// recorded. One instance serves one run; not safe for concurrent use.
type DurableReceipts struct {
	sink        RunSink
	manifestSHA string
	decisions   []store.SwarmRunPacket
}
