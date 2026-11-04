package swarm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// String renders the member identity as run/packet.
func (m MemberIdentity) String() string { return m.RunName + "/" + m.PacketID }

// Validate checks the claim's shape: outcome pass/fail and a
// non-empty evidence body.
func (c MemberClaim) Validate() error {
	if c.Identity.RunName == "" || c.Identity.PacketID == "" {
		return fmt.Errorf("swarm: member identity requires run and packet")
	}
	if c.GateOutcome != "pass" && c.GateOutcome != "fail" {
		return fmt.Errorf("swarm: member gate outcome must be pass or fail")
	}
	if strings.TrimSpace(c.Evidence) == "" {
		return fmt.Errorf("swarm: member evidence must not be empty")
	}
	return nil
}

// EvidenceSHA256 digests the evidence body; receipts store it as
// the payload digest.
func (c MemberClaim) EvidenceSHA256() string {
	sum := sha256.Sum256([]byte(c.Evidence))
	return hex.EncodeToString(sum[:])
}

// NewMemberAuthorizer builds the authorizer for one completed run.
func NewMemberAuthorizer(runName string, admitted []string) MemberAuthorizer {
	m := map[string]bool{}
	for _, id := range admitted {
		m[id] = true
	}
	return &admissionAuthorizer{runName: runName, admitted: m}
}

// AuthorizeMember rejects claims outside the run or unadmitted
// packets.
func (a *admissionAuthorizer) AuthorizeMember(claim MemberClaim) error {
	if claim.Identity.RunName != a.runName {
		return fmt.Errorf("swarm: claim run %q is not this run %q", claim.Identity.RunName, a.runName)
	}
	if !a.admitted[claim.Identity.PacketID] {
		return fmt.Errorf("swarm: member %s was not admitted by run %s", claim.Identity.PacketID, a.runName)
	}
	return nil
}

// EmitMemberReceipt validates and authorises one member claim and
// hands the durable receipt to the sink. Any admitted member may
// emit; the authorizer is the run's admission record.
func EmitMemberReceipt(auth MemberAuthorizer, claim MemberClaim, sink MemberReceiptSink) (MemberReceipt, error) {
	if err := claim.Validate(); err != nil {
		return MemberReceipt{}, err
	}
	if auth == nil {
		return MemberReceipt{}, fmt.Errorf("swarm: member authorizer is required")
	}
	if err := auth.AuthorizeMember(claim); err != nil {
		return MemberReceipt{}, err
	}
	rec := MemberReceipt{
		Identity:       claim.Identity,
		GateOutcome:    claim.GateOutcome,
		EvidenceSHA256: claim.EvidenceSHA256(),
	}
	if sink != nil {
		if err := sink.RecordMemberReceipt(rec); err != nil {
			return MemberReceipt{}, err
		}
	}
	return rec, nil
}
