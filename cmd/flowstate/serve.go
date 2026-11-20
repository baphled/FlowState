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

// servePorts maps the app read surface onto the server DTO ports.
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
	return server.Ports{ListSessions: list, InspectSession: inspect, ErrCode: app.ErrorCode}
}
func serveSession(r app.Session) server.Session {
	return server.Session{ID: r.ID, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Status: r.Status,
		Provider: r.Provider, ModelID: r.ModelID, Harness: r.Harness, ConfigSHA: r.ConfigSHA, ParentID: r.ParentID}
}
