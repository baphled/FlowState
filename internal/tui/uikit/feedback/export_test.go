package feedback

import "time"

// CalculateOpacityForTest exposes the unexported calculateOpacity method to
// external _test packages. The fade-in opacity calculation is deterministic
// and best verified directly rather than through Render output, so tests
// reach in via this shim.
func CalculateOpacityForTest(m *Modal) float64 {
	return m.calculateOpacity()
}

// SetFadeStartTimeForTest exposes the unexported fadeStartTime field so
// tests can simulate elapsed fade-in time without sleeping.
func SetFadeStartTimeForTest(m *Modal, t time.Time) {
	m.fadeStartTime = t
}

// MessageRotatorForTest returns the unexported messageRotator so tests can
// assert that SetMessageRotator wires the rotator correctly.
func MessageRotatorForTest(m *Modal) *LoadingMessageRotator {
	return m.messageRotator
}

// SpinnerForTest returns the unexported spinner so tests can read the
// current frame and verify spinner advancement.
func SpinnerForTest(m *Modal) *SimpleSpinner {
	return m.spinner
}

// WrapTextForTest exposes the unexported wrapText helper to external _test
// packages. Wrapping is a pure function whose behaviour is most cleanly
// verified directly rather than through Render output.
func WrapTextForTest(text string, width int) string {
	return wrapText(text, width)
}
