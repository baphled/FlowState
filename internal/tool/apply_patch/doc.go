// Package applypatch provides diff-based file patching for tools.
//
// This package handles:
//   - Parsing apply_patch-style unified diffs
//   - Applying patch hunks to files on disk
//   - Reporting conflicts and invalid patch input clearly
package applypatch
