package store

import (
	"context"
	"errors"
	"fmt"
)

// ErrDelegationCycle marks a parent_id chain that loops back on
// itself; it is defensive — creation cannot form a cycle, so one
// implies external corruption.
var ErrDelegationCycle = errors.New("store: delegation parent chain is cyclic")

// maxDelegationWalk bounds the defensive parent chain walk; the
// enforced delegation depth (app layer) is far smaller.
const maxDelegationWalk = 32

// DelegationChain returns the delegation ancestors of sessionID
// ordered root-first: a root session yields an empty slice, its child
// yields [root], a nested child yields [root, child]. Unknown ids are
// ErrUnknownSession; a loop or an impossibly long chain errors rather
// than reporting a partial chain.
func (s *Store) DelegationChain(ctx context.Context, sessionID string) ([]Session, error) {
	if _, err := s.GetSession(ctx, sessionID); err != nil {
		return nil, err
	}
	seen := map[string]bool{sessionID: true}
	var chain []Session
	cursor := sessionID
	for hop := 0; hop < maxDelegationWalk; hop++ {
		row, err := s.delegationParent(ctx, cursor)
		if err != nil {
			return nil, err
		}
		if row == nil {
			return chain, nil
		}
		if seen[row.ID] {
			return nil, fmt.Errorf("%w: session %s", ErrDelegationCycle, row.ID)
		}
		seen[row.ID] = true
		chain = append([]Session{*row}, chain...)
		cursor = row.ID
	}
	return nil, fmt.Errorf("%w: chain exceeds %d hops", ErrDelegationCycle, maxDelegationWalk)
}

// delegationParent loads the parent row of one session, or nil when
// the session is a root. Unknown ids are ErrUnknownSession.
func (s *Store) delegationParent(ctx context.Context, sessionID string) (*Session, error) {
	sess, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if sess.ParentID == "" {
		return nil, nil
	}
	return s.GetSession(ctx, sess.ParentID)
}
