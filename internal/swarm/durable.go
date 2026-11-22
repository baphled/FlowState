package swarm

import (
	"context"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/store"
)

// ErrDurableSinkMissing is returned when durable receipts have no
// store to persist through.
var ErrDurableSinkMissing = errors.New("swarm: durable receipts have no store")

// NewDurableReceipts builds a durable receipt sink pinned to the
// digest of the manifest bytes the run executes.
func NewDurableReceipts(sink RunSink, manifestSHA256 string) *DurableReceipts {
	return &DurableReceipts{sink: sink, manifestSHA: manifestSHA256}
}

// RecordAdmission buffers one admission decision; rows land in the
// store only when the complete run flushes at RecordGates, so a run
// that never reaches its gate decision leaves no partial record.
func (d *DurableReceipts) RecordAdmission(_ context.Context, _ string, p Packet, admitted bool, reason string) error {
	if d.sink == nil {
		return ErrDurableSinkMissing
	}
	d.decisions = append(d.decisions, store.SwarmRunPacket{
		PacketID: p.ID, Admitted: admitted, Reason: reason,
	})
	return nil
}

// RecordGates flushes the buffered run through the store: the gate
// decision becomes the run outcome and every buffered admission
// becomes one packet row. A run that was cancelled before every gate
// ran is not accepted, so it lands with the existing fail outcome;
// the store write honours ctx, which Run detaches from cancellation.
func (d *DurableReceipts) RecordGates(ctx context.Context, runID string, decision GatesDecision) error {
	if d.sink == nil {
		return ErrDurableSinkMissing
	}
	outcome := store.SwarmRunOutcomeFail
	if decision.Accepted {
		outcome = store.SwarmRunOutcomePass
	}
	run := store.SwarmRun{
		Name: runID, ManifestSHA256: d.manifestSHA, Outcome: outcome, Packets: d.decisions,
	}
	if _, err := d.sink.RecordSwarmRun(ctx, run); err != nil {
		return fmt.Errorf("record swarm run %s: %w", runID, err)
	}
	d.decisions = nil
	return nil
}

// compile-time contract: DurableReceipts is a RunReceipts sink.
var _ RunReceipts = (*DurableReceipts)(nil)
