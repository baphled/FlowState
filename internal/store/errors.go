package store

import "errors"

var (
	// ErrClosed is returned by operations on a closed Store.
	ErrClosed = errors.New("store: database is closed")

	// ErrFutureSchema is returned when the database holds a migration
	// version newer than the code understands (downgrades forbidden).
	ErrFutureSchema = errors.New("store: database schema is newer than this build")

	// ErrWriterExists is returned when a second concurrent event writer
	// is registered for the same session; per DATA-MODEL §5 concurrent
	// writers are rejected by design.
	ErrWriterExists = errors.New("store: session already has an event writer")

	// ErrSeqCollision is returned when appending an event would reuse an
	// already-committed seq for the session.
	ErrSeqCollision = errors.New("store: event seq already exists for session")

	// ErrSeqGap is returned by replay when the event seq for a session is
	// not contiguous from 0.
	ErrSeqGap = errors.New("store: event seq gap detected during replay")

	// ErrHashMismatch is returned when sha256(payload_json) does not
	// equal the stored payload_sha256, at write time or during replay.
	ErrHashMismatch = errors.New("store: payload_sha256 does not match payload_json")

	// ErrUnknownSession is returned when an event references a session id
	// that does not exist in sessions.
	ErrUnknownSession = errors.New("store: unknown session")

	// ErrNotPaused is returned when Resume is called on a writer whose
	// session is not currently paused.
	ErrNotPaused = errors.New("store: session is not paused")

	// ErrEmptyActor rejects empty event attribution actors.
	ErrEmptyActor = errors.New("store: event actor must not be empty")

	// ErrEmptyType rejects empty event types.
	ErrEmptyType = errors.New("store: event type must not be empty")
)
