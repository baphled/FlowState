// Package write provides a file system tool for writing file contents.
//
// This package implements a tool that:
//   - Writes content to files on the filesystem
//   - Creates parent directories as needed
//   - Validates paths to prevent directory traversal
//   - Returns structured results for agent consumption
//
// Security note: Paths are validated but this tool can still write to
// the filesystem; use appropriate permission controls.
package write
