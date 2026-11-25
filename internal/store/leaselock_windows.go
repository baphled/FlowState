//go:build windows

package store

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// fileIdentity returns the volume serial number and file index of the file at
// path, read with GetFileInformationByHandle on an open handle.
func fileIdentity(path string) (uint64, uint64, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = f.Close() }()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return 0, 0, err
	}
	return uint64(info.VolumeSerialNumber), uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow), nil
}

// openLockFile opens or creates the lock file inside root. The standard
// library shares READ|WRITE only, so an open lock file cannot be deleted.
func openLockFile(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDWR|os.O_CREATE, 0o600)
}

// tryLockHandle takes an exclusive LockFileEx range on the first byte without
// blocking; held is true on a lock violation.
func tryLockHandle(f *os.File) (held bool, err error) {
	ol := new(windows.Overlapped)
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return true, nil
	}
	return false, err
}

// unlockHandle releases the LockFileEx range.
func unlockHandle(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}
