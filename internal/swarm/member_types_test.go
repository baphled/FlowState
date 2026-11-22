package swarm_test

import (
	"context"

	"github.com/baphled/flowstate/internal/swarm"
)

// ctxMarkerKey keys a marker value on the caller context in member specs.
type ctxMarkerKey struct{}

// ctxRecordingSink is a MemberReceiptSink that keeps the context it was given.
type ctxRecordingSink struct {
	ctx context.Context
}

// RecordMemberReceipt remembers ctx and records nothing else.
func (s *ctxRecordingSink) RecordMemberReceipt(ctx context.Context, _ swarm.MemberReceipt) error {
	s.ctx = ctx
	return nil
}
