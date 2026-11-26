//go:build !unix

package tool

import "io/fs"

// linkCount reports 2 on every non-Unix platform, so every target takes the listing and the SameFile comparison:
// fail closed, at the cost of one listing per write. A Windows link count via GetFileInformationByHandle is a
// possible later optimisation.
func linkCount(fs.FileInfo) uint64 { return 2 }
