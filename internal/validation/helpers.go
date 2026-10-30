package validation

import "strconv"

// appendIf appends message to violations when cond is true and returns the
// resulting slice; a small helper keeping fail-closed checks one line each.
func appendIf(violations []string, cond bool, message string) []string {
	if cond {
		return append(violations, message)
	}
	return violations
}

// itoa renders value as its decimal string for violation labels.
func itoa(value int) string {
	return strconv.Itoa(value)
}
