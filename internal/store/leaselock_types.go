package store

import (
	"os"
	"sync"
)

// LockIdentity names a database file independently of the string it was
// opened with: the symlink-resolved absolute path plus the device and inode
// (Unix) or the volume serial number and file index (Windows). Two names for
// one inode (hard links) have different Paths and are unsupported.
type LockIdentity struct {
	// Path is the absolute path after filepath.EvalSymlinks.
	Path string
	// Device is the device (Unix) or volume serial number (Windows).
	Device uint64
	// Index is the inode (Unix) or file index (Windows).
	Index uint64
}

// LeaseLock is a held exclusive lock on one item. It is released by Release,
// or by the kernel when the holding process exits. A nil or released lock is
// safe to Release again. The handle owns an os.File: keep the LeaseLock
// reachable for the whole run (for example in the owning struct, or call
// runtime.KeepAlive after the work), because a dropped handle is finalised by
// the garbage collector, which closes the file and silently frees the lock.
type LeaseLock struct {
	mu      sync.Mutex
	item    string
	file    *os.File
	table   *LockTable
	tableID string
}

// LockTable is a process-local lock table keyed by item, for stores without a
// path. It excludes within one process only: no file, no identity. Each store
// owns its own table, so two stores never share lock state.
type LockTable struct {
	mu   sync.Mutex
	held map[string]struct{}
}
