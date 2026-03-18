// Package file provides a file system tool for reading and writing files.
//
// This package implements a tool that:
//   - Reads file contents with path validation
//   - Writes files with safety checks
//   - Validates paths to prevent directory traversal
//   - Returns structured results for agent consumption
//
// Security note: Paths are validated but this tool can still access
// the filesystem; use appropriate permission controls.
package file
