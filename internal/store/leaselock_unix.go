//go:build !windows

package store

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// fileIdentity returns the device and inode of the file at path.
func fileIdentity(path string) (uint64, uint64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, fmt.Errorf("unexpected stat type %T", fi.Sys())
	}
	return widen(st.Dev), widen(st.Ino), nil
}

// openLockFile opens or creates the lock file inside root. Go opens files
// close-on-exec, so a child process never inherits the lock. Opening a FIFO
// read-write does not block on Linux or macOS, so a FIFO planted at the path is
// opened and then refused by the caller as not a regular file.
func openLockFile(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDWR|os.O_CREATE, 0o600)
}

// tryLockHandle takes an exclusive flock without blocking; held is true when
// another open file description holds it.
func tryLockHandle(f *os.File) (held bool, err error) {
	return flockHandle(f, syscall.LOCK_EX|syscall.LOCK_NB)
}

// unlockHandle releases the flock.
func unlockHandle(f *os.File) error {
	_, err := flockHandle(f, syscall.LOCK_UN)
	return err
}

func flockHandle(f *os.File, how int) (bool, error) {
	rc, err := f.SyscallConn()
	if err != nil {
		return false, err
	}
	var ferr error
	if err := rc.Control(func(fd uintptr) {
		for {
			ferr = syscall.Flock(int(fd), how)
			if !errors.Is(ferr, syscall.EINTR) {
				return
			}
		}
	}); err != nil {
		return false, err
	}
	if errors.Is(ferr, syscall.EWOULDBLOCK) {
		return true, nil
	}
	return false, ferr
}

// widen converts a platform-sized stat field (its width differs by system) to uint64.
func widen[T ~int32 | ~uint32 | ~int64 | ~uint64](v T) uint64 { return uint64(v) }
