package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/server"
)

// defaultServeAddr is the loopback-only default listen address.
const defaultServeAddr = "127.0.0.1:7600"

func routeServe(ctx context.Context, args []string, stdout, stderr *os.File) int {
	addr, fl := defaultServeAddr, &flags{db: defaultDBPath}
	for i := 1; i < len(args); i++ {
		name, value, inline := strings.Cut(args[i], "=")
		if !inline {
			i++
			if i >= len(args) {
				writeError(stderr, fmt.Errorf("%w: flag %s requires a value", errUsageCmd, name))
				return exitUsage
			}
			value = args[i]
		}
		switch name {
		case "--addr":
			addr = value
		case "--db":
			fl.db, fl.dbSet = value, true
		case "--config":
			fl.configPath = value
		default:
			writeError(stderr, fmt.Errorf("%w: unknown flag %q", errUsageCmd, name))
			return exitUsage
		}
	}
	if code, done := applySessionConfig(fl, stderr); done {
		return code
	}
	return cmdServe(ctx, fl, addr, stdout, stderr)
}

// cmdServe implements `flowstate serve`: one shared store, one listener,
// graceful shutdown on ctx cancellation. The store opens before any listener,
// so a database failure exits 4 config_invalid without serving; the address
// is emitted as one JSON line.
func cmdServe(ctx context.Context, fl *flags, addr string, stdout, stderr *os.File) int {
	st, err := openStore(fl.db)
	if err != nil {
		writeError(stderr, fmt.Errorf("%w: cannot open database %q: %v", app.ErrConfigInvalid, fl.db, err))
		return exitConfigInvalid
	}
	defer st.Close()
	ln, lerr := net.Listen("tcp", addr)
	if lerr != nil {
		writeError(stderr, fmt.Errorf("%w: cannot listen on %q: %v", app.ErrUsage, addr, lerr))
		return exitUsage
	}
	if werr := writeJSON(stdout, map[string]any{"listen": ln.Addr().String()}); werr != nil {
		writeError(stderr, werr)
		return exitCodeFor(werr)
	}
	if serr := server.New(servePorts(st)).Serve(ctx, ln); serr != nil {
		writeError(stderr, serr)
		return exitCodeFor(serr)
	}
	return exitOK
}

// servePorts maps the app session surface onto the server DTO ports.
func servePorts(st *app.Store) server.Ports {
	list := func(ctx context.Context, status string) ([]server.Session, error) {
		rows, err := app.ListSessions(ctx, st, status)
		if err != nil {
			return nil, err
		}
		out := make([]server.Session, len(rows))
		for i, row := range rows {
			out[i] = serveSession(row)
		}
		return out, nil
	}
	inspect := func(ctx context.Context, id string) (server.Session, []server.Event, error) {
		sess, events, err := app.InspectSession(ctx, st, id)
		if err != nil {
			return server.Session{}, nil, err
		}
		out := make([]server.Event, len(events))
		for i, e := range events {
			out[i] = server.Event{SessionID: e.SessionID, Seq: e.Seq, CreatedAt: e.CreatedAt, Actor: e.Actor,
				Type: e.Type, Payload: json.RawMessage(e.PayloadJSON), PayloadSHA: e.PayloadSHA}
		}
		return serveSession(*sess), out, nil
	}
	closePort := func(ctx context.Context, id string) (server.Session, error) {
		return serveClose(ctx, st, id)
	}
	cancelPort := func(ctx context.Context, id string) (server.Session, error) {
		return serveCancel(ctx, st, id)
	}
	budgetPort := func(ctx context.Context, id string) (server.Budget, error) {
		return serveBudget(ctx, st, id)
	}
	setBudgetPort := func(ctx context.Context, id string, pool int64) (server.Budget, error) {
		return serveSetBudget(ctx, st, id, pool)
	}
	messagePort := func(ctx context.Context, id, sender, body string) (server.Message, error) {
		return serveMessage(ctx, st, id, sender, body)
	}
	return server.Ports{ListSessions: list, InspectSession: inspect,
		CloseSession: closePort, CancelSession: cancelPort, DiagnoseSession: func(ctx context.Context, id string) (server.Diagnosis, error) {
			return serveDiagnose(ctx, st, id)
		}, BudgetOverview: budgetPort, SetBudgetPool: setBudgetPort,
		SendSteeringMessage: messagePort, ErrCode: app.ErrorCode}
}

// serveClose wires POST /sessions/{id}/close onto app.CloseSession;
// a terminal close surfaces the app error for the shared table.
func serveClose(ctx context.Context, st *app.Store, id string) (server.Session, error) {
	sess, err := app.CloseSession(ctx, st, id)
	if err != nil {
		return server.Session{}, err
	}
	return serveSession(*sess), nil
}

// serveCancel wires POST /sessions/{id}/cancel onto app.CancelSession.
func serveCancel(ctx context.Context, st *app.Store, id string) (server.Session, error) {
	sess, err := app.CancelSession(ctx, st, id)
	if err != nil {
		return server.Session{}, err
	}
	return serveSession(*sess), nil
}

// serveDiagnose wires GET /sessions/{id}/doctor onto app.DiagnoseSession.
func serveDiagnose(ctx context.Context, st *app.Store, id string) (server.Diagnosis, error) {
	d, err := app.DiagnoseSession(ctx, st, id)
	if err != nil {
		return server.Diagnosis{}, err
	}
	return server.Diagnosis{SessionID: d.SessionID, Status: d.Status, Crashed: d.Crashed,
		RunActivity: d.RunActivity, PendingMessages: d.PendingMessages,
		Reserved: d.Reserved, Pool: d.Pool, Available: d.Available}, nil
}

// serveBudget wires GET /sessions/{id}/budget onto
// app.BudgetOverview; the snapshot stays unwrapped so an unknown
// session keeps its session_not_found mapping.
func serveBudget(ctx context.Context, st *app.Store, id string) (server.Budget, error) {
	snap, err := app.BudgetOverview(ctx, st, id)
	if err != nil {
		return server.Budget{}, err
	}
	return server.Budget{SessionID: id, Reserved: snap.Reserved,
		Pool: snap.Pool, Available: snap.Available}, nil
}

// serveSetBudget anchors the session's shared budget pool through the
// run command's seam: the row is loaded first (ResolveRunBudget takes
// the session), the positive pool anchors it exactly as `run --budget`
// does, and the response reports the updated overview.
func serveSetBudget(ctx context.Context, st *app.Store, id string, pool int64) (server.Budget, error) {
	sess, _, err := app.InspectSession(ctx, st, id)
	if err != nil {
		return server.Budget{}, err
	}
	if _, err := app.ResolveRunBudget(ctx, st, sess, pool); err != nil {
		return server.Budget{}, err
	}
	return serveBudget(ctx, st, id)
}

// serveMessage wires POST /sessions/{id}/message onto
// app.SendSteeringMessage. The app layer wraps every failure —
// unknown sessions and delegation-scope rejections — into ErrUsage,
// so the shared code table answers usage/400 for this endpoint; that
// wrapping is S2 app semantics and stays as-is.
func serveMessage(ctx context.Context, st *app.Store, id, sender, body string) (server.Message, error) {
	msg, err := app.SendSteeringMessage(ctx, st, id, sender, body)
	if err != nil {
		return server.Message{}, err
	}
	return server.Message{ID: msg.ID, SessionID: msg.SessionID, Sender: msg.Sender}, nil
}
func serveSession(r app.Session) server.Session {
	return server.Session{ID: r.ID, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Status: r.Status,
		Provider: r.Provider, ModelID: r.ModelID, Harness: r.Harness, ConfigSHA: r.ConfigSHA, ParentID: r.ParentID}
}
