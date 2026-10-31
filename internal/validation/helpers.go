package validation

// appendIf appends message to violations when cond is true and returns the
// resulting slice; a small helper keeping fail-closed checks one line each.
func appendIf(violations []string, cond bool, message string) []string {
	if cond {
		return append(violations, message)
	}
	return violations
}
