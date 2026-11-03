package app

import "sync"

// Hierarchical budget propagation types for the M2 swarm runtime.

// BudgetPool is a shared, concurrency-safe hierarchical budget that
// a parent session grants to child turns. Reserve returns the amount
// actually granted, which is the requested amount or whatever
// remains; zero means the pool is exhausted. Release returns unused
// budget to the pool (children report back what they consumed).
type BudgetPool struct {
	mu      sync.Mutex
	remain  int64
	granted int64
}
