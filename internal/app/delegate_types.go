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

// TypeDelegationStarted is the event RunDelegatedChild appends to the parent's
// log as soon as a child exists (before it runs). It is what lets crash recovery
// find the children of a dangling delegate call (ADR-009 D8).
const TypeDelegationStarted = "delegation.started"

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

// delegateLeasePrefix starts the lease owner of every delegated run: the prefix
// plus a fresh id, because a lease is re-granted to its own owner and one shared
// name would let two runs of a child both hold it.
const delegateLeasePrefix = "delegate:"

// runLeasePrefix starts the lease owner of a run that holds a session on its own behalf, such as
// the swarm coordinator: the prefix plus a fresh id, unique per run (ADR-011 D1).
const runLeasePrefix = "run:"

// delegateLegacyOwner is the exact lease owner an earlier build gave a delegated
// run, without the run id. Nothing else can ever release such a lease, so crash
// recovery clears it too.
const delegateLegacyOwner = "delegate"

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
	// NewRegistry builds the child's tool registry rooted at a directory, given the options its
	// filesystem tools must carry (the root run's protected hook directories, ADR-012 D20); nil uses
	// NewSessionToolRegistry. Tests inject fakes here: a builder that registers fs.write or fs.edit
	// must pass opts on to them, or the child can write the hook files the root cannot.
	NewRegistry func(workdir string, opts ...tool.FSOption) (*tool.Registry, error)
	// ParentEvents receives the delegation receipt when the caller already holds
	// the parent's event writer (the store allows one per session); nil opens a
	// writer on the parent for the append.
	ParentEvents tool.EventSink
	// Profiles are the profiles loaded for the run. A child whose profile lists
	// `delegate` offers exactly these to its own children.
	Profiles []agent.Profile
	// Compaction is the config's compaction object the child's loop applies, with the
	// profile's context_window.
	Compaction CompactionConfig
	// Hooks is the loaded root hook set (ADR-012 D22) the child's loop and its own delegate
	// tool inherit; nil is the empty set. A profile cannot change it.
	Hooks HookSet
	// ProtectedDirs is the root run's protected hook directories (ADR-012 D20, ProtectedHookDirs) the child's
	// fs.write and fs.edit refuse as the root's do, and its own children inherit. Nil is carried as an empty
	// list: the `.flowstate` name rule still applies, and only the identity rules are off. A profile cannot
	// change it.
	ProtectedDirs []string
	// allow restricts which profiles the delegating profile may spawn or resume;
	// nil allows every loaded profile (the root's own tool).
	allow *profileAllowance
	// limiter is the descendant cap of the root run, shared down the tree; nil
	// means no cap.
	limiter *descendantLimiter
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

// DelegateResumeRequest asks to continue an interrupted child of Parent
// (ADR-009 D8). Profile and Workdir of the embedded request are ignored: the
// profile is the loaded one the child recorded, and the child runs in the
// parent's working directory. Task, when set, is the follow-up user message.
type DelegateResumeRequest struct {
	DelegateRequest
	// ChildID is the child session to resume.
	ChildID string
	// Profiles are the profiles loaded for this run; the child's recorded
	// profile must be among them with an unchanged digest.
	Profiles []agent.Profile
}

// delegateRun is what the shared run-and-settle tail needs to know about one
// child: the session, the grant its loop draws from, the turns an earlier run
// already charged (zero for a fresh child) and whether it is a resume.
type delegateRun struct {
	owner   string
	child   *store.Session
	grant   int64
	prior   int64
	resumed bool
	// workdir is the child's resolved directory, the root of its own children's.
	workdir string
	// pool is the child's loop pool, nil when the child is unlimited: its own
	// turns and its descendants' grants are drawn from it.
	pool *BudgetPool
	// slot is the descendant slot the run holds while it executes.
	slot *runSlot
	// lease is the judged exec lease of the child, held from before its loop until its settlement
	// has returned (ADR-011 rule 8). A resume sets it when it claims the child; a fresh run
	// takes it in runAndSettleDelegated. Release is idempotent.
	lease *store.RunLease
}

// resumableLog is what a resume reads from the child's event log.
type resumableLog struct {
	profile *DelegateProfileRef
	turns   int64
}

// danglingChild is a child a dangling delegate call started. completed is true
// when its delegation.completed receipt was written: it finished, and only the
// result the call would have carried was lost.
type danglingChild struct {
	id        string
	status    string
	completed bool
}
