package app

import (
	"context"
	"fmt"

	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/swarm"
)

// SwarmEmitMemberReceipt emits one member receipt for a completed
// run, persisted to the receipts database at dbPath. It is the
// cmd-facing seam so cmd imports only app.
func SwarmEmitMemberReceipt(dbPath, runName string, admitted []string, claim swarm.MemberClaim) (swarm.MemberReceipt, error) {
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
	return swarm.EmitMemberReceipt(auth, claim, swarmMemberReceipts{rec: rec})
}

// RecordMemberReceipt maps the receipt onto the store record and persists
// it. swarm.MemberReceiptSink carries no context and SwarmEmitMemberReceipt
// has none to pass (cmd calls it without one), so the write runs under
// context.Background().
func (s swarmMemberReceipts) RecordMemberReceipt(r swarm.MemberReceipt) error {
	return s.rec.RecordMemberReceipt(context.Background(), store.MemberReceiptRecord{
		RunName: r.Identity.RunName, PacketID: r.Identity.PacketID,
		GateOutcome: r.GateOutcome, EvidenceSHA256: r.EvidenceSHA256,
	})
}
