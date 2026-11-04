package app

import "github.com/baphled/flowstate/internal/storage"

// runEventsTo adapts RunEvents callbacks onto the receipts sink.
type runEventsTo struct {
	rec   *storage.SwarmReceipts
	runID string
}
