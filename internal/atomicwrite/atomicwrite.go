// Package atomicwrite provides crash-safe file writes via the
// write-temp-then-rename-with-fsync pattern.
//
// The motivating failure mode for FlowState is auth/credential persistence:
// callers such as the Anthropic OAuth refresh path and the encrypted token
// store would corrupt their target file if the process was killed (or the
// host lost power) between os.WriteFile's truncate and its final write.
// The user is then logged out, sometimes silently, the next time the file
// is read.
//
// File writes the bytes to a uniquely-named temp file in the same directory
// as path, fsync's the temp file's contents to disk, atomically renames it
// over path, and fsync's the parent directory so the rename itself is
// durable across a crash. Either the old bytes or the new bytes are visible
// at path — never an empty file, never partial content.
package atomicwrite

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// File writes data to path atomically with the requested permission bits.
//
// Expected:
//   - path is an absolute or relative target file path. Its parent
//     directory must already exist; callers control directory creation
//     (typically via os.MkdirAll with their preferred mode) before
//     invoking File.
//   - data is the bytes to persist. May be empty.
//   - perm is the file mode applied to the temp file (and thus to path
//     after rename). Callers typically pass 0o600 for credentials.
//
// Returns:
//   - nil on success.
//   - An error if the temp file cannot be created, written, fsync'd, or
//     renamed. On error, path retains its previous contents (or remains
//     absent if it did not previously exist).
//
// Side effects:
//   - Creates a temp file in filepath.Dir(path) with a random suffix.
//   - On any failure after temp-file creation, removes the temp file.
//   - On success, renames the temp file over path and fsync's the parent
//     directory.
func File(path string, data []byte, perm os.FileMode) error {
	if path == "" {
		return errors.New("atomicwrite: empty path")
	}

	dir := filepath.Dir(path)
	tmpPath, err := writeTempFile(dir, filepath.Base(path), data, perm)
	if err != nil {
		return err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomicwrite: renaming temp file: %w", err)
	}

	// fsync the parent directory so the rename is durable. Failures here
	// are non-fatal on platforms where directories cannot be fsync'd; the
	// rename has already succeeded and the data is on disk via the
	// temp-file fsync.
	if d, dirErr := os.Open(dir); dirErr == nil {
		_ = d.Sync()
		_ = d.Close()
	}

	return nil
}

// writeTempFile creates a uniquely-named temp file in dir, writes data to
// it, fsync's the contents, and returns the temp file path. Permission
// bits are applied via chmod after creation so behaviour does not depend
// on the caller's umask.
func writeTempFile(
	dir, base string,
	data []byte,
	perm os.FileMode,
) (string, error) {
	suffix, err := randomSuffix()
	if err != nil {
		return "", fmt.Errorf("atomicwrite: generating temp suffix: %w", err)
	}

	tmpPath := filepath.Join(dir, base+".atomicwrite-"+suffix)

	f, err := os.OpenFile(
		tmpPath,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		perm,
	)
	if err != nil {
		return "", fmt.Errorf("atomicwrite: creating temp file: %w", err)
	}

	// Ensure the file ends up with exactly perm even if umask masked it.
	if chmodErr := f.Chmod(perm); chmodErr != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("atomicwrite: chmod temp file: %w", chmodErr)
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("atomicwrite: writing temp file: %w", err)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("atomicwrite: fsync temp file: %w", err)
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("atomicwrite: closing temp file: %w", err)
	}

	return tmpPath, nil
}

// randomSuffix returns 8 hex characters of cryptographic randomness, used
// to disambiguate concurrent temp files writing to the same target path.
func randomSuffix() (string, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
