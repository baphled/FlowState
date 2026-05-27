package carbonyl

import (
	"net/http"

	"github.com/baphled/flowstate/internal/agent"
	flowapp "github.com/baphled/flowstate/internal/app"
)

// AppAdapter wraps *flowapp.App to satisfy the AppInterface used by
// the Carbonyl runner. It bridges the gap between the CLI's *app.App
// and the interface that carbonyl.Run expects.
type AppAdapter struct {
	App *flowapp.App
}

// SetAgentManifest resolves the agent manifest from the registry and
// sets it on the engine. The chat command calls this before invoking
// carbonyl.Run so the engine is fully wired by the time the renderer
// boots.
func (a *AppAdapter) SetAgentManifest(agentID string) {
	if agentID == "" || a.App.Registry == nil {
		return
	}
	if manifest, ok := a.App.Registry.Get(agentID); ok {
		a.App.Engine.SetManifest(*manifest)
	}
}

// EventBus returns nil. The Carbonyl runner's publishResumedEvent is a
// no-op placeholder; a future change will thread the real event bus
// through once the event bus interface is stabilised.
func (a *AppAdapter) EventBus() EventBus {
	return nil
}

// SessionMgr returns the session manager wrapped as a
// SessionRegistrar. Returns nil when the app has no session manager.
func (a *AppAdapter) SessionMgr() SessionRegistrar {
	mgr := a.App.SessionMgr()
	if mgr == nil {
		return nil
	}
	return &sessionMgrAdapter{mgr: mgr}
}

// SessionsDir returns the directory where session data is persisted.
func (a *AppAdapter) SessionsDir() string {
	return a.App.SessionsDir()
}

// APIServer returns the HTTP API server wrapped as a carbonyl.APIServer.
// Returns nil when the app has no API server configured.
func (a *AppAdapter) APIServer() APIServer {
	if a.App.API == nil {
		return nil
	}
	return &apiServerAdapter{srv: a.App.API}
}

// sessionMgrAdapter adapts *session.Manager to the SessionRegistrar
// interface required by the Carbonyl runner.
type sessionMgrAdapter struct {
	mgr sessionManager
}

func (s *sessionMgrAdapter) RegisterSession(sessionID string, agentID string) {
	s.mgr.RegisterSession(sessionID, agentID)
}

// apiServerAdapter adapts the *api.Server type to the APIServer
// interface required by the Carbonyl runner.
type apiServerAdapter struct {
	srv apiServerConcrete
}

func (a *apiServerAdapter) Handler() http.Handler {
	return a.srv.Handler()
}

// sessionManager is the minimal interface that *session.Manager must
// satisfy for the adapter.
type sessionManager interface {
	RegisterSession(id, agentID string)
}

// apiServerConcrete is the minimal interface that *api.Server must
// satisfy for the adapter.
type apiServerConcrete interface {
	Handler() http.Handler
}

// Compile-time interface satisfaction checks.
var (
	_ AppInterface     = (*AppAdapter)(nil)
	_ SessionRegistrar = (*sessionMgrAdapter)(nil)
	_ APIServer        = (*apiServerAdapter)(nil)
)

// Blank identifier usage to ensure the agent package remains linked.
// SetAgentManifest uses agent.Manifest via Registry.Get.
var _ agent.Manifest
