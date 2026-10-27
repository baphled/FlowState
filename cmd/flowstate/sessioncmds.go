package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// cmdNew implements `flowstate session new`: creates a session and
// prints its JSON record (id, status) on stdout. Exit 0 on success;
// 2 usage; 4 config invalid.
func cmdNew(ctx context.Context, st *store.Store, fl *flags, stdout, stderr *os.File) int {
	if fl.provider == "" || fl.model == "" {
		writeError(stderr, fmt.Errorf("%w: --provider and --model are required", session.ErrUsage))
		return exitUsage
	}
	task, err := readTask(fl.task)
	if err != nil {
		writeError(stderr, err)
		return exitUsage
	}
	if task == "" {
		writeError(stderr, fmt.Errorf("%w: --task must not be empty", session.ErrUsage))
		return exitUsage
	}
	sess, err := session.Create(ctx, st, fl.provider, fl.model, session.DefaultHarness, "", task)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if err := writeJSON(stdout, map[string]any{"id": sess.ID, "status": sess.Status}); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	return exitOK
}

// readTask resolves --task, reading the file when given as @path.
func readTask(task string) (string, error) {
	if task == "" || !strings.HasPrefix(task, "@") {
		return task, nil
	}
	b, err := os.ReadFile(strings.TrimPrefix(task, "@"))
	if err != nil {
		return "", fmt.Errorf("%w: cannot read task file %q: %v", session.ErrUsage, task, err)
	}
	return string(b), nil
}

// cmdList implements `flowstate session list`: one JSON object per
// session on stdout, or a table. Exit 0; 2 on an invalid --status.
func cmdList(ctx context.Context, st *store.Store, fl *flags, stdout, stderr *os.File) int {
	sessions, err := session.List(ctx, st, fl.status)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if fl.json || true { // JSON lines is the M1 machine-parseable default
		for _, s := range sessions {
			if err := writeJSON(stdout, sessionJSON(s)); err != nil {
				writeError(stderr, err)
				return exitCodeFor(err)
			}
		}
	}
	return exitOK
}

// cmdEvents implements `flowstate session events <id>`: dumps the
// ordered event log as JSON lines. Exit 0; 1 unknown session; 7
// replay mismatch.
func cmdEvents(ctx context.Context, st *store.Store, id string, stdout, stderr *os.File) int {
	if id == "" {
		writeError(stderr, fmt.Errorf("%w: session id required", session.ErrUsage))
		return exitUsage
	}
	_, events, err := session.Inspect(ctx, st, id)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	for _, e := range events {
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
	return exitOK
}

// cmdClose implements `flowstate session close <id>`: transitions to
// cancelled and prints the state JSON. Exit 0; 6 already terminal; 1
// unknown session.
func cmdClose(ctx context.Context, st *store.Store, id string, stdout, stderr *os.File) int {
	if id == "" {
		writeError(stderr, fmt.Errorf("%w: session id required", session.ErrUsage))
		return exitUsage
	}
	sess, err := session.Close(ctx, st, id)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if err := writeJSON(stdout, map[string]any{"id": sess.ID, "status": sess.Status}); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	return exitOK
}

// timeFormat is the wire timestamp format (RFC3339Nano UTC).
const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"

// sessionJSON renders a session record as its wire object.
func sessionJSON(s store.Session) map[string]any {
	return map[string]any{
		"id":             s.ID,
		"created_at_utc": s.CreatedAt.UTC().Format(timeFormat),
		"updated_at_utc": s.UpdatedAt.UTC().Format(timeFormat),
		"status":         s.Status,
		"provider":       s.Provider,
		"model_id":       s.ModelID,
		"harness":        s.Harness,
		"config_sha256":  s.ConfigSHA,
	}
}

// writeJSON marshals v as one line of JSON on w.
func writeJSON(w *os.File, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode output: %w", err)
	}
	_, err = w.Write(append(b, '\n'))
	return err
}
