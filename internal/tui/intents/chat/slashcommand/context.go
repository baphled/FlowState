package slashcommand

import (
	"github.com/baphled/flowstate/internal/agent"
	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/provider"
)

// CommandContext is the narrow surface every command handler needs.
//
// The chat intent populates a CommandContext when dispatching a slash
// command; nil-valued handles are valid for commands that do not depend
// on that capability (e.g. /clear ignores SessionLister entirely). Each
// handler must guard against the handles it consumes being nil so a
// minimally-wired test or embedded shell can still register and exercise
// commands.
type CommandContext struct {
	// MessageWiper clears the chat view's message buffer when invoked.
	// /clear consumes this; others leave it nil.
	MessageWiper MessageWiper
	// SystemMessageWriter pushes a system-role message into the chat
	// view. /help renders its output through this; /plans uses it to
	// dump the selected plan as a read-only message.
	SystemMessageWriter SystemMessageWriter
	// SessionResumer switches the chat to the given session ID. /sessions
	// consumes this. nil disables the /sessions handler.
	SessionResumer SessionResumer
	// SessionLister supplies the list shown by the /sessions sub-picker.
	SessionLister SessionLister
	// PlanLister supplies the list shown by the /plans sub-picker.
	PlanLister PlanLister
	// PlanFetcher loads the full plan body for /plans rendering.
	PlanFetcher PlanFetcher
	// AgentRegistry supplies the list shown by the /agent sub-picker.
	AgentRegistry *agent.Registry
	// AgentSwitcher applies an agent manifest to the running engine.
	AgentSwitcher AgentSwitcher
	// ProviderLister enumerates the configured providers consulted by
	// /model.
	ProviderLister ProviderLister
	// ModelSwitcher swaps the chat model in the running engine.
	ModelSwitcher ModelSwitcher
	// Registry is the parent registry, exposed so /help can iterate the
	// command set without a separate handle.
	Registry *Registry
}

// MessageWiper is the narrow capability /clear consumes.
type MessageWiper interface {
	// ClearMessages wipes the chat view's message buffer.
	ClearMessages()
}

// SystemMessageWriter is the narrow capability /help and /plans consume
// to dump multi-line text into the chat as a system-role message.
type SystemMessageWriter interface {
	// AddSystemMessage appends a system-role message to the chat view.
	AddSystemMessage(content string)
}

// SessionResumer is the narrow capability /sessions consumes.
type SessionResumer interface {
	// ResumeSession switches the chat to the given session ID.
	ResumeSession(sessionID string)
}

// SessionLister enumerates saved sessions for /sessions sub-picker.
type SessionLister interface {
	// List returns metadata for every saved session, most-recent first.
	List() []contextpkg.SessionInfo
}

// PlanLister enumerates plan summaries for /plans sub-picker.
type PlanLister interface {
	// List returns a summary for every saved plan.
	List() ([]plan.Summary, error)
}

// PlanFetcher loads a full plan document for /plans rendering.
type PlanFetcher interface {
	// Get retrieves the full plan body for the given ID.
	Get(id string) (*plan.File, error)
}

// AgentSwitcher applies an agent manifest to the running engine.
type AgentSwitcher interface {
	// SetManifest replaces the engine's active agent manifest. Existing
	// engine call sites accept this signature, so the chat intent can
	// adapt directly.
	SetManifest(manifest agent.Manifest)
}

// ProviderLister enumerates the registered providers for /model.
type ProviderLister interface {
	// List returns the registered provider names.
	List() []string
	// Get returns the provider for inspection (model lists, etc.).
	Get(name string) (provider.Provider, error)
}

// ModelSwitcher swaps the chat model in the running engine.
type ModelSwitcher interface {
	// SetModelPreference updates the engine's preferred provider/model.
	SetModelPreference(providerName string, modelName string)
}
