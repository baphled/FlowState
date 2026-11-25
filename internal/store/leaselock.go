package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// maxLockAttempts bounds the retries when the lock file is replaced between
// the open and the descriptor-versus-path check.
const maxLockAttempts = 8

// ErrLockHeld reports that another holder, in this or another process, holds
// the lock for the item.
var ErrLockHeld = errors.New("store: lock held")

// ErrLockUnstable reports that the lock file kept being replaced between the
// open and the descriptor-versus-path check, so no lock was taken. A caller
// may retry; it is distinct from ErrLockHeld and from a plain I/O failure.
var ErrLockUnstable = errors.New("store: lock file unstable")

// ErrLockNotRegular reports that the lock path names something other than a
// regular file (a FIFO, a device); it is refused rather than locked.
var ErrLockNotRegular = errors.New("store: lock path is not a regular file")

// errLockNoTable reports a path-less acquire without a table.
var errLockNoTable = errors.New("store: a store without a path needs a lock table")

// lockAfterOpenHook is called after the lock file is opened and locked and
// before the descriptor-versus-path check. It exists so a spec can replace the
// file at that moment; production code never sets it.
var lockAfterOpenHook = func(*os.Root, string) {}

// ResolveLockIdentity resolves a database path to its LockIdentity: the
// absolute path, then filepath.EvalSymlinks, then the device and inode (or
// volume serial and file index) of the resolved file. The file must exist.
// Hard links are unsupported (ADR-011 rule 1): two names for one inode get the
// same device and index but different Path, so their lock directories differ
// and they do not exclude each other.
func ResolveLockIdentity(dbPath string) (LockIdentity, error) {
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return LockIdentity{}, fmt.Errorf("store: resolve %q: %w", dbPath, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return LockIdentity{}, fmt.Errorf("store: resolve %q: %w", dbPath, err)
	}
	dev, idx, err := fileIdentity(resolved)
	if err != nil {
		return LockIdentity{}, fmt.Errorf("store: identify %q: %w", resolved, err)
	}
	return LockIdentity{Path: resolved, Device: dev, Index: idx}, nil
}

// LockPath returns the directory beside the database that holds the lock files,
// the fan-out subdirectory name and the lock file name for item. The name is a
// hash of the device, inode and item, so no item can walk out of the directory
// and a database replaced at the same path normally gets fresh lock files. An
// inode can be reused after deletion; that yields the same name, which errs
// towards reporting a holder alive. The directory comes from Path, so the
// identity separates files by name and inode together, not by inode alone.
func LockPath(id LockIdentity, item string) (dir, fan, name string) {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", id.Device, id.Index, item)))
	name = hex.EncodeToString(sum[:])
	return id.Path + ".locks", name[:2], name
}

// AcquireItemLock takes the exclusive lock for item without blocking. A
// store without a path (dbPath empty) uses table, which the caller owns (one
// per store, so stores never share state); otherwise the lock is a file lock
// keyed by the database's identity and table is not consulted (it may be nil).
// It returns ErrLockHeld when the lock is held.
func AcquireItemLock(table *LockTable, dbPath, item string) (*LeaseLock, error) {
	if dbPath == "" {
		if table == nil {
			return nil, errLockNoTable
		}
		return table.TryLock(item)
	}
	id, err := ResolveLockIdentity(dbPath)
	if err != nil {
		return nil, err
	}
	return TryLockFile(id, item)
}

// TryLockFile takes the exclusive file lock for item without blocking and
// returns ErrLockHeld when it is held. The descriptor is close-on-exec. After
// locking, the open descriptor is compared with the path; if the file was
// replaced, the attempt is repeated on the new file, and after maxLockAttempts
// the error wraps ErrLockUnstable. A non-regular file at the path yields
// ErrLockNotRegular. A lock file deleted while held leaves a second acquirer
// free to lock a new file (two holders); FlowState never deletes it.
func TryLockFile(id LockIdentity, item string) (*LeaseLock, error) {
	dir, fan, name := LockPath(id, item)
	fanDir := filepath.Join(dir, fan)
	if err := os.MkdirAll(fanDir, 0o750); err != nil {
		return nil, fmt.Errorf("store: create lock directory %q: %w", fanDir, err)
	}
	root, err := os.OpenRoot(fanDir)
	if err != nil {
		return nil, fmt.Errorf("store: open lock directory %q: %w", fanDir, err)
	}
	defer func() { _ = root.Close() }()
	for range maxLockAttempts {
		l, retry, err := tryLockOnce(root, name, item)
		if err != nil {
			return nil, fmt.Errorf("store: lock file %q: %w", filepath.Join(fanDir, name), err)
		}
		if !retry {
			return l, nil
		}
	}
	return nil, fmt.Errorf("store: lock file %q kept being replaced: %w", filepath.Join(fanDir, name), ErrLockUnstable)
}

func tryLockOnce(root *os.Root, name, item string) (*LeaseLock, bool, error) {
	f, err := openLockFile(root, name)
	if err != nil {
		return nil, false, err
	}
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		_ = f.Close()
		if err == nil {
			err = ErrLockNotRegular
		}
		return nil, false, err
	}
	held, err := tryLockHandle(f)
	if err != nil || held {
		_ = f.Close()
		if held {
			return nil, false, ErrLockHeld
		}
		return nil, false, err
	}
	lockAfterOpenHook(root, name)
	same, err := sameAsPath(f, root, name)
	if err != nil || !same {
		_ = unlockHandle(f)
		_ = f.Close()
		return nil, err == nil, err
	}
	return &LeaseLock{item: item, file: f}, false, nil
}

// sameAsPath reports whether the open file is still the file the path names.
func sameAsPath(f *os.File, root *os.Root, name string) (bool, error) {
	fi, err := f.Stat()
	if err != nil {
		return false, err
	}
	pi, err := root.Stat(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return os.SameFile(fi, pi), nil
}

// NewLockTable returns an empty process-local lock table.
func NewLockTable() *LockTable {
	return &LockTable{held: map[string]struct{}{}}
}

// TryLock takes the lock for item without blocking and returns ErrLockHeld
// when this table already holds it.
func (t *LockTable) TryLock(item string) (*LeaseLock, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.held[item]; ok {
		return nil, ErrLockHeld
	}
	t.held[item] = struct{}{}
	return &LeaseLock{item: item, table: t, tableID: item}, nil
}

// Item returns the item the lock was taken for.
func (l *LeaseLock) Item() string { return l.item }

// Release unlocks and closes the lock. It is idempotent and safe on nil.
func (l *LeaseLock) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.table != nil {
		l.table.mu.Lock()
		delete(l.table.held, l.tableID)
		l.table.mu.Unlock()
		l.table = nil
		return nil
	}
	if l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	uerr := unlockHandle(f)
	cerr := f.Close()
	return errors.Join(uerr, cerr)
}
