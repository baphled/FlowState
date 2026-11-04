package app

// swarmMemberReceipts adapts the receipts database as a
// MemberReceiptSink.
type swarmMemberReceipts struct{ rec *dbRunReceipts }
