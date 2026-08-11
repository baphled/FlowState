// Package store defines the cluster-ready session-record persistence
// interface for internal/auth.
//
// This package handles:
//   - Providing the Store interface pinned by the API Auth Track plan
//   - Shipping MemoryStore as the full v1 default implementation
//   - Shipping RedisStore and PostgresStore as stub implementations for v1
//   - Enabling drop-in replacement of real Redis or Postgres impls in v3
package store
