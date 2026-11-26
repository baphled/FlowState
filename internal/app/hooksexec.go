package app

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// unsafeHookMode reports a group- or world-writable file (Unix only: Windows
// has no equivalent and is not checked).
func unsafeHookMode(info fs.FileInfo) bool {
	return runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0
}

// readHookFile reads a hook file. A missing file or directory is not an error
// (found false). The file is opened through os.OpenRoot on its directory, so a
// symlink leaving it is refused; it must be a regular file, not
// group- or world-writable on Unix, and at most MaxHooksFileBytes. Nothing
// blocks: the directory is os.Stat-ed first (a symlink to a directory is fine)
// because os.OpenRoot hangs on a FIFO, and the file is opened non-blocking.
func readHookFile(path string) (data []byte, found bool, err error) {
	dir := filepath.Dir(path)
	dirInfo, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !dirInfo.IsDir() {
		return nil, false, fmt.Errorf("hook directory is not a directory (%s)", dir)
	}
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = root.Close() }()
	f, err := openHookFile(root, filepath.Base(path))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("hook file is not a regular file (%s)", info.Mode().Type())
	}
	if unsafeHookMode(info) {
		return nil, false, fmt.Errorf("hook file is group- or world-writable (%s)", info.Mode().Perm())
	}
	data, err = io.ReadAll(io.LimitReader(f, MaxHooksFileBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > MaxHooksFileBytes {
		return nil, false, fmt.Errorf("hook file is larger than %d bytes", MaxHooksFileBytes)
	}
	return data, true, nil
}

// checkExecutable resolves a command hook's argv[0] and applies the load-time
// checks: after symlink resolution it must be a regular file, not group- or
// world-writable, and carry an execute bit (Unix). A relative path (one with a
// separator) resolves against baseDir and must also resolve inside it; an
// absolute path or a bare name looked up on PATH is the operator's choice and
// has no such clause. It returns the resolved path.
func checkExecutable(argv0, baseDir string) (string, error) {
	var path string
	relative := false
	switch {
	case filepath.IsAbs(argv0):
		path = argv0
	case strings.ContainsRune(argv0, '/') || strings.ContainsRune(argv0, filepath.Separator):
		path, relative = filepath.Join(baseDir, argv0), true
	default:
		found, err := exec.LookPath(argv0)
		if err != nil {
			return "", fmt.Errorf("executable %q: %w", argv0, err)
		}
		path = found
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("executable %q: %w", argv0, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("executable %q: %w", argv0, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("executable %q is not a regular file (%s)", argv0, info.Mode().Type())
	}
	if unsafeHookMode(info) {
		return "", fmt.Errorf("executable %q is group- or world-writable (%s)", argv0, info.Mode().Perm())
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("executable %q lacks the execute bit", argv0)
	}
	if relative {
		if err := insideDir(resolved, baseDir); err != nil {
			return "", fmt.Errorf("executable %q: %w", argv0, err)
		}
	}
	return resolved, nil
}

// insideDir refuses a resolved path that is not below dir (itself resolved).
func insideDir(resolved, dir string) error {
	base, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(base, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("resolves outside the hook file's directory %s", base)
	}
	return nil
}
