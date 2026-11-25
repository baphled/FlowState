//go:build !linux && !darwin && !windows

package store

// readMachineID has no system id on this OS, so the host name fallback applies.
var readMachineID = func() string { return "" }
