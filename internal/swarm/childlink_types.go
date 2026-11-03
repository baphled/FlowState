package swarm

// Child-session linkage for the M2 runtime: a child session records
// the parent session and the admitted packet that authorised its
// creation, so orchestration can resolve the scope-bound tree.

// ChildLink binds one child session to its parent session and the
// admission packet that authorised it. ParentSessionID and PacketID
// are required; ChildSessionID is assigned at creation time.
type ChildLink struct {
	// ParentSessionID is the session that delegated the child.
	ParentSessionID string
	// ChildSessionID is the delegated session's id.
	ChildSessionID string
	// PacketID is the admitted packet authorising the delegation.
	PacketID string
}
