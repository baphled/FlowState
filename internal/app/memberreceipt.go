package app

import (
	"context"
	"fmt"

	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/swarm"
)

// SwarmEmitMemberReceipt emits one member receipt for a completed
// run, persisted to the receipts database at dbPath. It is the
// cmd-facing seam so cmd imports only app. A finished packet's receipt is
// recorded even when ctx is cancelled (see swarm.EmitMemberReceipt).
func SwarmEmitMemberReceipt(ctx context.Context, dbPath, runName string, admitted []string, claim swarm.MemberClaim) (swarm.MemberReceipt, error) {
	db, err := openReceipts(dbPath)
	if err != nil {
		return swarm.MemberReceipt{}, err
	}
	defer func() { _ = db.Close() }()
	rec, err := store.NewSwarmReceiptLog(db.db())
	if err != nil {
		return swarm.MemberReceipt{}, fmt.Errorf("app: initialise swarm persistence: %w", err)
	}
	auth := swarm.NewMemberAuthorizer(runName, admitted)
	return swarm.EmitMemberReceipt(ctx, auth, claim, swarmMemberReceipts{rec: rec})
}

// RecordMemberReceipt maps the receipt onto the store record and persists
// it under ctx, which swarm.EmitMemberReceipt has already detached from the
// caller's cancellation.
func (s swarmMemberReceipts) RecordMemberReceipt(ctx context.Context, r swarm.MemberReceipt) error {
	return s.rec.RecordMemberReceipt(ctx, store.MemberReceiptRecord{
		RunName: r.Identity.RunName, PacketID: r.Identity.PacketID,
		GateOutcome: r.GateOutcome, EvidenceSHA256: r.EvidenceSHA256,
	})
}
