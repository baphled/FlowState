//go:build !unix

package app

import "os"

// openHookFile opens name inside root read-only. Platforms without Unix FIFOs
// need no non-blocking flag.
func openHookFile(root *os.Root, name string) (*os.File, error) {
	return root.Open(name)
}
