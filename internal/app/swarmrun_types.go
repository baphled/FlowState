package app

import "github.com/baphled/flowstate/internal/store"

// runReceiptLog adapts the store's swarm receipt log to the
// swarm.RunReceipts sink, mapping swarm types onto plain store types.
type runReceiptLog struct{ log *store.SwarmReceiptLog }

// runEventsTo adapts RunEvents callbacks onto the receipts sink.
type runEventsTo struct {
	rec   *runReceiptLog
	runID string
}
