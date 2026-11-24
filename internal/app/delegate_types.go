package app

import (
	"sync"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// TypeDelegationCompleted is the receipt event RunDelegatedChild appends to the
// parent's log when a delegation ends, whatever its status.
const TypeDelegationCompleted = "delegation.completed"

// TypeDelegationFailed is the final event of a child that failed outside the
// provider path (the loop records provider failures itself).
const TypeDelegationFailed = "delegation.failed"

// Delegation statuses a DelegateResult reports. A refused budget reports
// DelegateStatusFailed with no child.
const (
	// DelegateStatusCompleted marks a child that finished its loop.
	DelegateStatusCompleted = "completed"
	// DelegateStatusFailed marks a child that failed or was never granted a budget.
	DelegateStatusFailed = "failed"
	// DelegateStatusCancelled marks a child cancelled with its parent.
	DelegateStatusCancelled = "cancelled"
)

// delegateLeaseAgent is the lease owner recorded for delegated child runs.
const delegateLeaseAgent = "delegate"

// DelegateProfileRef names the profile a child ran under: recorded in the
// child's session.delegated payload and in the delegation receipt.
type DelegateProfileRef struct {
	// Name is the profile name.
	Name string `json:"name"`
	// Path is the file the profile was read from.
	Path string `json:"path"`
	// SHA256 is the hex digest of that file's bytes.
	SHA256 string `json:"sha256"`
}

// DelegateRequest carries one delegation: the parent it runs under, the
// profile that shapes the child and the task the child receives.
type DelegateRequest struct {
	// Store holds the sessions, events and reservations.
	Store *store.Store
	// Parent is the delegating session; its provider, model, harness and config
	// hash are inherited. A terminal Parent, or one the store holds as terminal,
	// is refused.
	Parent *store.Session
	// Streamer is the provider the parent uses; the child shares it.
	Streamer provider.Streamer
	// Pool is the parent's turn pool. When set, the child's grant (the profile's
	// MaxTurns) is drawn from it, reserved in the store too, and the unspent
	// part is returned on settlement. Nil means the parent is unlimited and no
	// reservation is made. It must be a limited pool: Release on an unlimited
	// pool would turn it into a limited one.
	Pool *BudgetPool
	// ParentWorkdir is the parent's tool working directory; it must exist.
	ParentWorkdir string
	// Profile is the validated agent profile.
	Profile agent.Profile
	// Task is the child's first user message.
	Task string
	// Workdir optionally narrows the child to the parent's directory or a
	// subdirectory of it; empty means the parent's. Anything else is refused.
	Workdir string
	// NewRegistry builds the child's tool registry rooted at a directory; nil
	// uses NewSessionToolRegistry. Tests inject fakes here.
	NewRegistry func(workdir string) (*tool.Registry, error)
	// ParentEvents receives the delegation receipt when the caller already holds
	// the parent's event writer (the store allows one per session); nil opens a
	// writer on the parent for the append.
	ParentEvents tool.EventSink
	// BatchIndex is the entry's position in a parallel `tasks` call; when set the
	// receipt carries it as "index". Nil for a single delegation.
	BatchIndex *int
}

// DelegateResult is the outcome the caller turns into tool output. A failed or
// cancelled child is a result, not an error.
type DelegateResult struct {
	// ChildID is the child session; empty when no child was created.
	ChildID string
	// Status is DelegateStatusCompleted, DelegateStatusFailed or DelegateStatusCancelled.
	Status string
	// Turns is the number of provider turns the child used.
	Turns int64
	// ToolCalls is the number of tool calls the child requested.
	ToolCalls int64
	// FinalContent is the child's final assistant message.
	FinalContent string
	// Reason says why the child did not complete; empty when it completed.
	Reason string
	// Partial marks a child that did not complete but produced text.
	Partial bool
}

// receiptLock is one parent's receipt lock and the number of users holding or
// waiting for it.
type receiptLock struct {
	mu    sync.Mutex
	users int
}

// receiptLockTable hands out per-parent receipt locks and forgets idle ones.
type receiptLockTable struct {
	mu    sync.Mutex
	locks map[string]*receiptLock
}
