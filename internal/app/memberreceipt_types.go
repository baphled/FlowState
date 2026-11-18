package app

import "github.com/baphled/flowstate/internal/storage"

// swarmMemberReceipts adapts the receipts database as a
// MemberReceiptSink.
type swarmMemberReceipts struct{ rec *storage.SwarmReceipts }
