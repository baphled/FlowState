package qdrant

import (
	"errors"
	"fmt"
	"net/http"
)

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

// IsCollectionNotFound reports whether err originates from a Qdrant
// HTTP 404 response, which Qdrant uses both for missing collections and
// for missing points within an existing collection. The auto-ensure
// path treats any 404 from a write as "collection probably missing,
// try to create it" — the subsequent CollectionExists check filters
// out spurious creates.
//
// Expected:
//   - err may be nil, a *Error, or any wrapped error chain.
//
// Returns:
//   - true when the chain includes a *Error with StatusCode == 404.
//
// Side effects:
//   - None.
func IsCollectionNotFound(err error) bool {
	if err == nil {
		return false
	}
	var qerr *Error
	if !errors.As(err, &qerr) {
		return false
	}
	return qerr.StatusCode == http.StatusNotFound
}
