package app

import (
	"fmt"

	"github.com/baphled/flowstate/internal/storage"
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
	rec, err := storage.NewSwarmReceipts(db.db())
	if err != nil {
		return swarm.MemberReceipt{}, fmt.Errorf("app: initialise swarm persistence: %w", err)
	}
	auth := swarm.NewMemberAuthorizer(runName, admitted)
	return swarm.EmitMemberReceipt(auth, claim, swarmMemberReceipts{rec: rec})
}

// RecordMemberReceipt forwards the member receipt to the persistence adapter.
func (s swarmMemberReceipts) RecordMemberReceipt(r swarm.MemberReceipt) error {
	return s.rec.RecordMemberReceipt(r)
}
