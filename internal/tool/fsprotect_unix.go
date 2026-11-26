//go:build unix

package tool

import (
	"io/fs"
	"syscall"
)

// linkCount returns the number of hard links to info's file (2 when it cannot tell, which is the fail-closed answer).
func linkCount(info fs.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return widen(st.Nlink)
	}
	return 2
}

// widen converts a link count of any unsigned width (int16 on aix, uint16 on darwin, uint64 on linux).
func widen[T ~int16 | ~uint16 | ~uint32 | ~uint64](n T) uint64 { return uint64(n) }
