package app

import "github.com/baphled/flowstate/internal/store"

// swarmMemberReceipts adapts the receipts database as a
// MemberReceiptSink.
type swarmMemberReceipts struct{ rec *store.SwarmReceiptLog }
