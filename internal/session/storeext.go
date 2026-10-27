// Package session provides session lifecycle persistence on top of
// internal/store: creation with an initial turn.start event, listing,
// inspection of the verified event log, and terminal close. It wraps
// the store package; the legacy in-memory turn primitives remain for
// callers that need a provider-agnostic harness without storage.
package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/baphled/flowstate/internal/store"
)

// Errors surfaced to the CLI layer; each maps onto the closed error
// code set of specs/CLI-AND-API-CONTRACT.md §2.
var (
	// ErrUsage maps to code `usage` (exit 2).
	ErrUsage = errors.New("usage")

	// ErrTerminal maps to code `session_terminal` (exit 6): the
	// session is already in a terminal state.
	ErrTerminal = errors.New("session_terminal")
)

// terminalStatuses are the session states that reject further state
// transitions (DATA-MODEL §7).
var terminalStatuses = map[string]bool{
	"completed": true, "cancelled": true, "failed": true,
}

// validStatusFilters are the statuses accepted by `--status`.
var validStatusFilters = map[string]bool{
	"active": true, "paused": true, "completed": true,
	"cancelled": true, "failed": true,
}

// Event types emitted by this package.
const (
	TypeTurnStart        = "turn.start"
	TypeSessionCancelled = "session.cancelled"
)

// Actors used for attribution.
const (
	ActorUser   = "user"
	ActorEngine = "engine"
)

// Create persists a new session row (status active) and appends the
// initial `turn.start` event with the task payload. It returns the
// created session record. Empty provider, model or task is ErrUsage.
func Create(ctx context.Context, st *store.Store, provider, modelID, harness, configSHA, task string) (*store.Session, error) {
	if provider == "" || modelID == "" || task == "" {
		return nil, fmt.Errorf("%w: provider, model and task must not be empty", ErrUsage)
	}
	if harness == "" {
		harness = DefaultHarness
	}
	id, err := NewID()
	if err != nil {
		return nil, fmt.Errorf("generate session id: %w", err)
	}
	sess, err := st.CreateSession(ctx, id, provider, modelID, harness, configSHA)
	if err != nil {
		return nil, err
	}
	w, err := st.NewEventWriter(ctx, sess.ID)
	if err != nil {
		return nil, err
	}
	defer w.Close()
	if _, err := w.Append(ctx, ActorUser, TypeTurnStart, map[string]any{"task": task}); err != nil {
		return nil, err
	}
	return sess, nil
}

// List returns sessions ordered by creation time (oldest first). A
// non-empty status filters on it; an invalid status is ErrUsage.
func List(ctx context.Context, st *store.Store, status string) ([]store.Session, error) {
	if status != "" && !validStatusFilters[status] {
		return nil, fmt.Errorf("%w: unknown status %q", ErrUsage, status)
	}
	rows, err := st.DB().QueryContext(ctx,
		`SELECT id, created_at_utc, updated_at_utc, status, provider, model_id, harness, config_sha256
		 FROM sessions`+whereStatus(status)+` ORDER BY created_at_utc, id`, statusArgs(status)...)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	var out []store.Session
	for rows.Next() {
		var s store.Session
		var created, updated string
		if err := rows.Scan(&s.ID, &created, &updated, &s.Status, &s.Provider, &s.ModelID, &s.Harness, &s.ConfigSHA); err != nil {
			return nil, fmt.Errorf("list scan: %w", err)
		}
		if err := parseTimes(&s, created, updated); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func whereStatus(status string) string {
	if status == "" {
		return ""
	}
	return " WHERE status=?"
}

func statusArgs(status string) []any {
	if status == "" {
		return nil
	}
	return []any{status}
}

// Inspect loads a session and its verified event log (seq contiguous,
// payload hashes verified by store.Replay) for CLI `session events`.
func Inspect(ctx context.Context, st *store.Store, id string) (*store.Session, []store.RecordedEvent, error) {
	sess, err := st.GetSession(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	events, err := st.Replay(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return sess, events, nil
}

// Close transitions an active or paused session to cancelled, appends
// a `session.cancelled` event in the same logical transition, and
// returns the updated session. Closing a terminal session is
// ErrTerminal; an unknown id is store.ErrUnknownSession.
func Close(ctx context.Context, st *store.Store, id string) (*store.Session, error) {
	sess, err := st.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if terminalStatuses[sess.Status] {
		return nil, fmt.Errorf("%w: session %s is %s", ErrTerminal, id, sess.Status)
	}
	w, err := st.NewEventWriter(ctx, id)
	if err != nil {
		return nil, err
	}
	defer w.Close()
	payload := map[string]any{"status": "cancelled"}
	if _, err := w.Append(ctx, ActorEngine, TypeSessionCancelled, payload); err != nil {
		return nil, err
	}
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE sessions SET status='cancelled', updated_at_utc=? WHERE id=?`,
		mustISO(now()), id); err != nil {
		return nil, fmt.Errorf("cancel session %s: %w", id, err)
	}
	sess.Status = "cancelled"
	return sess, nil
}

// now returns the current UTC time; a package var so tests could
// substitute a clock if needed.
var now = func() time.Time { return time.Now().UTC() }

// mustISO formats t; it never fails because the layout is fixed.
func mustISO(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// parseTimes fills CreatedAt/UpdatedAt from stored RFC3339Nano text.
func parseTimes(s *store.Session, created, updated string) error {
	var err error
	if s.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return fmt.Errorf("parse created_at %q: %w", created, err)
	}
	if s.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return fmt.Errorf("parse updated_at %q: %w", updated, err)
	}
	return nil
}

// NewID returns a fresh UUIDv7 session id (DATA-MODEL §2 requires
// UUIDv7 primary keys).
func NewID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// DefaultHarness is the harness attribution recorded when a caller
// does not supply an observed one.
const DefaultHarness = "flowstate-cli"
