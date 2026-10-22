// Package store defines the cluster-ready Snapshot-record persistence
// interface for internal/provider/quota.
//
// This package handles:
//   - Providing the Store interface for TokenSpend snapshot persistence
//   - Shipping MemoryStore as the full v1 default implementation
//   - Shipping RedisStore and PostgresStore as stub implementations for v1
//   - Enabling drop-in replacement of real Redis or Postgres impls in v3
package store
