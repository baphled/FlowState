//go:build !unix

package skill

import "os"

// openFile opens rel inside r read-only; platforms without FIFOs need no flag.
func openFile(r *os.Root, rel string) (*os.File, error) {
	return r.Open(rel)
}
