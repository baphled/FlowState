// Package runner defines shared interface types used across internal packages
// to avoid import cycles.
//
// This package handles:
//   - Housing the AutoresearchRunner seam so internal/engine avoids importing internal/cli
//   - Defining AutoresearchOpts for operator-settable programmatic run inputs
package runner
