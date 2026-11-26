//go:build unix

package skill

import (
	"os"
	"syscall"
)

// openFile opens rel inside r read-only without blocking, so a FIFO swapped in
// after the mode check returns at once and is rejected by the post-open check.
func openFile(r *os.Root, rel string) (*os.File, error) {
	return r.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
