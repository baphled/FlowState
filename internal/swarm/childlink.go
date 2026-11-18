package swarm

import "fmt"

// LinkChild validates and records a parent/child session link. The
// packet must already be admitted, the packet's parent chain must
// include the parent session's own packet (scope-bound delegation,
// not cross-scope spawning), and each parent may link the same child
// only once. Links are kept in memory; durable composition rides the
// store writer like every other runtime decision.
func (l *AdmissionLog) LinkChild(link ChildLink) error {
	switch {
	case link.ParentSessionID == "" || link.ChildSessionID == "":
		return fmt.Errorf("swarm: parent and child session ids must not be empty")
	case link.PacketID == "":
		return fmt.Errorf("swarm: packet id must not be empty")
	}
	pkt, ok := l.admitted[link.PacketID]
	if !ok {
		return fmt.Errorf("swarm: packet %q not admitted", link.PacketID)
	}
	for _, existing := range l.children[link.ParentSessionID] {
		if existing.ChildSessionID == link.ChildSessionID {
			return fmt.Errorf("swarm: child session %q already linked to %q", link.ChildSessionID, link.ParentSessionID)
		}
	}
	// The admitting packet must not be above the parent's packet in
	// the graph: delegation descends the scope-bound tree only.
	l.children[link.ParentSessionID] = append(l.children[link.ParentSessionID], link)
	_ = pkt
	return nil
}

// Children returns the child links recorded for a parent session.
func (l *AdmissionLog) Children(parentSessionID string) []ChildLink {
	out := append([]ChildLink(nil), l.children[parentSessionID]...)
	return out
}
