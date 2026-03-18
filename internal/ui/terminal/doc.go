// Package terminal provides terminal detection and information utilities.
//
// # Overview
//
// The terminal package handles terminal capability detection including
// color support, unicode support, and terminal size. It provides
// fallback options for limited terminals.
//
// # Features
//
//   - Color support detection (true color, 256 color, 16 color, none)
//   - Terminal size queries
//   - Unicode support detection
//   - Terminal capability reports
//
// # Usage
//
//	info := terminal.GetInfo()
//	if info.SupportsTrueColor {
//	    // Use rich colors
//	}
package terminal
