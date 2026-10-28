package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/registry"
	"github.com/baphled/flowstate/internal/runtime"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// providerResolver is the CLI's provider factory seam; tests swap it
// for an httptest-backed adapter, production uses the registry.
var providerResolver = defaultProviderResolver

// defaultProviderResolver resolves a provider by name through the
// registry (the only place a concrete adapter is selected).
func defaultProviderResolver(name string) (provider.Provider, error) {
	return registry.Resolve(name)
}

// errProvider marks provider failures (code provider_error, exit 3
// per CLI-AND-API-CONTRACT §1).
var errProvider = fmt.Errorf("provider_error")

// exitProviderError is the stable provider-failure exit code.
const exitProviderError = 3

// cmdRun implements `flowstate session run <id>`: runs one bounded
// provider turn, projects the persisted events as JSONL on stdout and
// ends with a terminal event envelope. Exit 0 completed; 1 unknown
// session; 2 usage; 3 provider error; 6 already terminal.
func cmdRun(ctx context.Context, st *store.Store, id string, stdout, stderr *os.File) int {
	if id == "" {
		writeError(stderr, fmt.Errorf("%w: session id required", session.ErrUsage))
		return exitUsage
	}
	sess, err := st.GetSession(ctx, id)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if sess.Status != "active" && sess.Status != "paused" {
		writeError(stderr, fmt.Errorf("%w: session %s is %s", session.ErrTerminal, id, sess.Status))
		return exitTerminal
	}
	p, err := providerResolver(sess.Provider)
	if err != nil {
		writeError(stderr, fmt.Errorf("%w: %v", errProvider, err))
		return exitProviderError
	}
	streamer, ok := p.(provider.Streamer)
	if !ok {
		writeError(stderr, fmt.Errorf("%w: provider %s does not support streaming turns", errProvider, sess.Provider))
		return exitProviderError
	}
	res, err := runtime.Turn(ctx, st, sess, streamer)
	_ = res
	evts, rerr := st.Replay(ctx, id)
	if rerr != nil {
		writeError(stderr, rerr)
		return exitCodeFor(rerr)
	}
	for _, e := range evts {
		if err := writeJSON(stdout, map[string]any{
			"session_id":     e.SessionID,
			"seq":            e.Seq,
			"created_at_utc": e.CreatedAt.UTC().Format(timeFormat),
			"actor":          e.Actor,
			"type":           e.Type,
			"payload":        json.RawMessage(e.PayloadJSON),
			"payload_sha256": e.PayloadSHA,
		}); err != nil {
			writeError(stderr, err)
			return exitCodeFor(err)
		}
	}
	if err != nil {
		writeError(stderr, fmt.Errorf("%w: %v", errProvider, err))
		return exitProviderError
	}
	return exitOK
}
