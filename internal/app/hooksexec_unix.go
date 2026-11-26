//go:build unix

package app

import (
	"os"
	"syscall"
)

// openHookFile opens name inside root read-only without blocking, so a FIFO
// with no writer is returned at once and rejected by the regular-file check
// instead of hanging the loader.
func openHookFile(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
