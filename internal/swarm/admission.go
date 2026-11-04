package swarm

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// sha64 matches a 64-char lowercase hex digest.
var sha64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// sha256Hex is the lowercase hex SHA-256 of body.
func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// NewAdmissionLog builds an empty admission log.
func NewAdmissionLog() *AdmissionLog {
	return &AdmissionLog{
		admitted: map[string]Packet{},
		children: map[string][]ChildLink{},
	}
}

// Admission-control typed errors.
var (
	// ErrInvalidPacket marks a packet failing structural validation.
	ErrInvalidPacket = errors.New("swarm: invalid packet")
	// ErrHashMismatch marks a packet whose source body hash does not
	// match its declared SourceSHA256.
	ErrHashMismatch = errors.New("swarm: source hash mismatch")
	// ErrPrerequisite marks admission against a prerequisite packet
	// that is not itself admitted yet.
	ErrPrerequisite = errors.New("swarm: prerequisite not admitted")
	// ErrCycle marks a parent/child edge that would close a cycle.
	ErrCycle = errors.New("swarm: parent cycle")
	// ErrScopeEscape marks a child scope outside its parent's scope.
	ErrScopeEscape = errors.New("swarm: scope escape")
	// ErrDuplicate marks readmission of an already-admitted packet.
	ErrDuplicate = errors.New("swarm: packet already admitted")
)

// Admit validates one packet against the log and, on success,
// records it as admitted: the source body's SHA-256 must equal the
// declared SourceSHA256, every prerequisite must already be
// admitted, the ParentID edge must not close a cycle and the packet
// scope must stay inside the parent scope (scope-bound graph).
func (l *AdmissionLog) Admit(p Packet, sourceBody []byte) error {
	if err := ValidatePacket(p); err != nil {
		return err
	}
	if err := l.checkHash(p, sourceBody); err != nil {
		return err
	}
	if err := l.checkPrerequisites(p); err != nil {
		return err
	}
	if err := l.checkGraph(p); err != nil {
		return err
	}
	l.admitted[p.ID] = p
	return nil
}

// Admitted reports whether packet id has been admitted.
func (l *AdmissionLog) Admitted(id string) bool {
	_, ok := l.admitted[id]
	return ok
}

// ValidatePacket enforces the packet's structural invariants: ids,
// 64-hex source digest, non-empty scope and task, and no
// self-parenting.
func ValidatePacket(p Packet) error {
	switch {
	case p.ID == "":
		return fmt.Errorf("%w: id must not be empty", ErrInvalidPacket)
	case !sha64.MatchString(p.SourceSHA256):
		return fmt.Errorf("%w: source_sha256 must be 64 hex chars", ErrInvalidPacket)
	case p.Scope == "" || p.Task == "":
		return fmt.Errorf("%w: scope and task must not be empty", ErrInvalidPacket)
	case p.ParentID == p.ID:
		return fmt.Errorf("%w: packet cannot be its own parent", ErrInvalidPacket)
	}
	return nil
}

// checkHash pins the declared source digest to the actual bytes.
func (l *AdmissionLog) checkHash(p Packet, body []byte) error {
	sum := sha256Hex(body)
	if sum != p.SourceSHA256 {
		return fmt.Errorf("%w: declared %s got %s", ErrHashMismatch, p.SourceSHA256, sum)
	}
	return nil
}

// checkPrerequisites requires every prerequisite packet to already
// be admitted (admission control before delegation).
func (l *AdmissionLog) checkPrerequisites(p Packet) error {
	for _, pre := range p.Prerequisites {
		if !l.Admitted(pre) {
			return fmt.Errorf("%w: %s (for %s)", ErrPrerequisite, pre, p.ID)
		}
	}
	return nil
}

// checkGraph walks the admitted parent chain before evaluating scope.
// A cycle remains a cycle even when the proposed edge also widens scope.
func (l *AdmissionLog) checkGraph(p Packet) error {
	if p.ParentID == "" {
		return nil
	}
	parent, ok := l.admitted[p.ParentID]
	if !ok {
		return fmt.Errorf("%w: parent %s not admitted", ErrPrerequisite, p.ParentID)
	}
	cursor := parent
	for cursor.ID != p.ID {
		if cursor.ParentID == "" {
			break
		}
		next, ok := l.admitted[cursor.ParentID]
		if !ok {
			break
		}
		if next.ID == p.ID {
			return fmt.Errorf("%w: %s is already an ancestor of %s", ErrCycle, p.ID, cursor.ID)
		}
		cursor = next
	}
	if cursor.ID == p.ID {
		return fmt.Errorf("%w: %s revisited", ErrCycle, p.ID)
	}
	if !scopeWithin(p.Scope, parent.Scope) {
		return fmt.Errorf("%w: %s outside parent scope %s", ErrScopeEscape, p.Scope, parent.Scope)
	}
	return nil
}

// scopeWithin reports whether child equals parent or extends it with
// a '.'-separated segment.
func scopeWithin(child, parent string) bool {
	return child == parent || len(child) > len(parent) && strings.HasPrefix(child, parent) && child[len(parent)] == '.'
}

// SHA256HexForTest exposes the internal digest helper to the test
// suite (fixture support only; not a production seam).
func SHA256HexForTest(body []byte) string { return sha256Hex(body) }
