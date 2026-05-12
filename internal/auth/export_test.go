package auth

import (
	"context"

	"github.com/baphled/flowstate/internal/auth/store"
)

// StampRecordCtx is a test-only helper that exposes withRecord to the
// _test package so CSRF and middleware specs can inject a Record into
// the request context without standing up the full RequireSession chain.
//
// Lives under _test build tag (export_test.go) — never compiled into
// production binaries.
func StampRecordCtx(ctx context.Context, rec *store.Record) context.Context {
	return withRecord(ctx, rec)
}
