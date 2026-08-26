// Package read provides a file system tool for reading file contents.
//
// This package implements a tool that:
//   - Reads file contents from the filesystem
//   - Validates paths to prevent directory traversal
//   - Returns structured results for agent consumption
//
// Security note: Paths are validated but this tool can still access
// the filesystem; use appropriate permission controls.
package read
