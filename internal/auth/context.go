package auth

import (
	"context"
	"net/http"

	"github.com/baphled/flowstate/internal/auth/store"
)

// ctxKey is the unexported context-key type used by RequireSession to
// attach the authenticated Record to the request. Per the standard Go
// idiom (effective-go §"Context"), context keys are unexported typed
// constants to prevent cross-package collisions.
type ctxKey int

const (
	// ctxRecord is the key under which RequireSession stamps the
	// authenticated session Record. Downstream middleware
	// (RequireCSRFRecordBound) and handlers read it back via
	// requestRecord / RecordFrom.
	ctxRecord ctxKey = iota
)

// withRecord returns a copy of ctx carrying rec under ctxRecord. Used
// internally by RequireSession; not exported because the auth surface
// MUST be the sole writer of this key.
func withRecord(ctx context.Context, rec *store.Record) context.Context {
	return context.WithValue(ctx, ctxRecord, rec)
}

// requestRecord returns the Record attached to r's context by
// RequireSession, or nil if none is present. Internal-only — the
// exported accessor is RecordFrom (below) for HTTP handlers consuming
// the authenticated principal.
func requestRecord(r *http.Request) *store.Record {
	if r == nil {
		return nil
	}
	val := r.Context().Value(ctxRecord)
	if val == nil {
		return nil
	}
	rec, ok := val.(*store.Record)
	if !ok {
		return nil
	}
	return rec
}

// RecordFrom returns the authenticated session Record attached to r's
// context by RequireSession, or nil if the request hasn't been
// authenticated.
//
// HTTP handlers behind the protected middleware chain MAY call this to
// access the principal id, mode, or CSRF token. Handlers that need the
// principal as a hard precondition (no nil result) should rely on
// RequireSession having executed first — its 401 short-circuit closes
// the nil path.
func RecordFrom(r *http.Request) *store.Record {
	return requestRecord(r)
}
