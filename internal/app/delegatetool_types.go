package app

import (
	"encoding/json"
	"sync"

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

// delegateArgs is the strict argument object of one delegate call. Tasks is
// decoded whole and validated before any child starts.
type delegateArgs struct {
	Agent  string          `json:"agent"`
	Task   string          `json:"task"`
	Cwd    string          `json:"cwd"`
	Tasks  json.RawMessage `json:"tasks"`
	Resume string          `json:"resume"`
	// batch is Tasks once decoded and validated.
	batch []delegateTaskArg
}

// delegateTaskArg is one entry of the `tasks` array.
type delegateTaskArg struct {
	Agent string `json:"agent"`
	Task  string `json:"task"`
	Cwd   string `json:"cwd"`
}

// DelegateTaskResult is one entry of a parallel call's result. A child that did
// not complete is an entry with its status and reason, not an error: the model
// needs the results of the siblings.
type DelegateTaskResult struct {
	// Index is the entry's position in the `tasks` array.
	Index int `json:"index"`
	// Agent is the profile name the entry asked for.
	Agent string `json:"agent"`
	// Child is the child session id; empty when no child was created.
	Child string `json:"child,omitempty"`
	// Status is completed, failed or cancelled.
	Status string `json:"status"`
	// Turns is the number of provider turns the child used.
	Turns int64 `json:"turns"`
	// Text is the child's final message (partial for a child that did not
	// complete), capped at 50 KB and 2000 lines.
	Text string `json:"text,omitempty"`
	// Reason says why the entry did not complete.
	Reason string `json:"reason,omitempty"`
}

// DelegateParallelResult is the JSON a parallel call returns: one entry per
// task, in input order.
type DelegateParallelResult struct {
	// Results holds one entry per task, in the order of the `tasks` array.
	Results []DelegateTaskResult `json:"results"`
}

// delegateSlot is what one parallel entry leaves behind for the caller.
type delegateSlot struct {
	result DelegateTaskResult
	// fatal is an infrastructure failure that must end the run.
	fatal error
	// panicked and panicValue record a panic recovered in the entry's goroutine.
	panicked   bool
	panicValue any
}

// lockedSink serialises Append on a writer shared by concurrent children.
type lockedSink struct {
	mu    sync.Mutex
	inner tool.EventSink
}

// delegateParentEventsKey is the context key under which the loop passes the
// parent's per-call event writer to the tool.
type delegateParentEventsKey struct{}

// appendFailureSink marks every Append failure of the wrapped sink as an event
// persistence failure, so the loop terminates the run as it does for tool.call.
type appendFailureSink struct {
	inner tool.EventSink
}
