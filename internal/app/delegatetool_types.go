package app

import (
	"encoding/json"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// DelegateToolConfig binds a DelegateTool to one parent run.
type DelegateToolConfig struct {
	// Store holds the parent's session, events and reservations.
	Store *store.Store
	// Parent is the delegating session.
	Parent *store.Session
	// Streamer is the parent's provider; children share it.
	Streamer provider.Streamer
	// Pool is the parent's turn pool; nil means the parent is unlimited.
	Pool *BudgetPool
	// Workdir is the parent's tool working directory.
	Workdir string
	// Profiles are the profiles loaded for this run; the tool offers exactly these.
	Profiles []agent.Profile
}

// DelegateTool is the `delegate` tool: it runs one profile-driven child of the
// parent run and reports the outcome to the parent model (ADR-009). It is
// qualified by its concrete type in SessionToolDefinitions, as the builtins are,
// so another tool cannot take its schema by reusing its name. It reads the
// parent's event writer from the invocation context (see withParentEvents) and
// never opens a second one.
type DelegateTool struct {
	cfg DelegateToolConfig
}

// DelegateToolResult is the JSON a completed child returns to the parent model.
type DelegateToolResult struct {
	// Child is the child session id.
	Child string `json:"child"`
	// Status is the child's status, always "completed" for a result.
	Status string `json:"status"`
	// Turns is the number of provider turns the child used.
	Turns int64 `json:"turns"`
	// Text is the child's final message, capped at 50 KB and 2000 lines.
	Text string `json:"text"`
}

// delegateArgs is the strict argument object of one delegate call. Tasks and
// Resume are decoded only to be refused with a clear message.
type delegateArgs struct {
	Agent  string          `json:"agent"`
	Task   string          `json:"task"`
	Cwd    string          `json:"cwd"`
	Tasks  json.RawMessage `json:"tasks"`
	Resume json.RawMessage `json:"resume"`
}

// delegateParentEventsKey is the context key under which the loop passes the
// parent's per-call event writer to the tool.
type delegateParentEventsKey struct{}

// appendFailureSink marks every Append failure of the wrapped sink as an event
// persistence failure, so the loop terminates the run as it does for tool.call.
type appendFailureSink struct {
	inner tool.EventSink
}
