// Package coordination provides a shared key-value store for cross-agent context
// sharing during delegation chains.
//
// This package handles:
//   - Defining the Store interface for key-value operations (Get, Set, List, Delete).
//   - Providing a thread-safe in-memory implementation (MemoryStore) using sync.RWMutex.
//   - Supporting chain ID namespace isolation via key prefix conventions.
//
// Keys follow the convention {chainID}/{keyname} to scope data to a specific
// delegation chain. The store itself is a flat key-value map; namespace isolation
// is achieved by callers using consistent key prefixes.
package coordination
