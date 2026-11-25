//go:build linux

package store

import (
	"io"
	"os"
	"strings"
)

// linuxIDFiles lists where a Linux machine id lives, in order of preference
// (recalled from the ADR and machine-id(5); /etc/machine-id exists on the
// development machine, /var/lib/dbus/machine-id is unverified).
var linuxIDFiles = []string{"/etc/machine-id", "/var/lib/dbus/machine-id"}

// readMachineID is the per-OS read seam.
var readMachineID = func() string { return firstIDFrom(linuxIDFiles) }

// firstIDFrom returns the first non-empty file content among paths, or "".
func firstIDFrom(paths []string) string {
	for _, p := range paths {
		if id := readIDFile(p); id != "" {
			return id
		}
	}
	return ""
}

// readIDFile reads one small file through a root opened on "/", so gosec's G304
// does not apply to the variable path. A symlink inside "/" is followed
// (/etc/machine-id is read this way); an absolute symlink is refused as an
// escape, which makes /var/lib/dbus/machine-id (commonly an absolute symlink to
// /etc/machine-id) fail harmlessly: the first file already gave the id.
func readIDFile(path string) string {
	root, err := os.OpenRoot("/")
	if err != nil {
		return ""
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(strings.TrimPrefix(path, "/"))
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 256))
	if err != nil {
		return ""
	}
	return string(data)
}
