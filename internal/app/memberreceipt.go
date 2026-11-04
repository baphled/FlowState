package app

import (
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
	rec := &dbRunReceipts{db: db.db()}
	auth := swarm.NewMemberAuthorizer(runName, admitted)
	return swarm.EmitMemberReceipt(auth, claim, swarmMemberReceipts{rec: rec})
}

// RecordMemberReceipt persists one member receipt row.
func (s swarmMemberReceipts) RecordMemberReceipt(r swarm.MemberReceipt) error {
	_, err := s.rec.db.Exec(
		`INSERT INTO swarm_member_receipts (run_name, packet_id, gate_outcome, evidence_sha256, recorded_at_utc)
		 VALUES (?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
		r.Identity.RunName, r.Identity.PacketID, r.GateOutcome, r.EvidenceSHA256)
	if err != nil {
		return err
	}
	return nil
}
