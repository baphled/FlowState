//go:build windows

package store

import "golang.org/x/sys/windows/registry"

// readMachineGUID reads the registry value; tests replace it.
var readMachineGUID = func() (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`,
		registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", err
	}
	defer func() { _ = k.Close() }()
	v, _, err := k.GetStringValue("MachineGuid")
	return v, err
}

// readMachineID is the per-OS read seam: HKLM\SOFTWARE\Microsoft\Cryptography
// MachineGuid. RECALLED, UNVERIFIED on a real Windows machine.
var readMachineID = func() string {
	v, err := readMachineGUID()
	if err != nil {
		return ""
	}
	return v
}
