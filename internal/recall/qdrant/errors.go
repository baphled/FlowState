package qdrant

import "fmt"

// Error represents an error returned by the Qdrant HTTP API.
type Error struct {
	StatusCode int
	Message    string
}

// Error returns a formatted error string including the HTTP status code.
//
// Expected:
//   - The receiver is a valid Error with StatusCode and Message populated.
//
// Returns:
//   - A string in the form "qdrant: HTTP <status>: <message>".
//
// Side effects:
//   - None.
func (e *Error) Error() string {
	return fmt.Sprintf("qdrant: HTTP %d: %s", e.StatusCode, e.Message)
}

// Is reports whether target is a *Error, enabling errors.Is matching.
//
// Expected:
//   - target is any error value.
//
// Returns:
//   - true if target is a *Error, false otherwise.
//
// Side effects:
//   - None.
func (e *Error) Is(target error) bool {
	_, ok := target.(*Error)
	return ok
}
