package app

import (
	"context"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// ExecLease is a held exec lease the loop is given by its caller (ADR-011 rule 8). *store.RunLease
// satisfies it. The loop only fences it: it never acquires or releases a lease it was given,
// because the caller keeps it held until the post-loop state writers have returned and releases
// it afterwards. The interface therefore has no Release.
type ExecLease interface {
	// Item is the leased item, for example "exec:<session id>".
	Item() string
	// Owner is the lease owner the fence is checked for.
	Owner() string
	// Token is the fencing token the lease was granted with.
	Token() int64
	// Fence fails with store.ErrLeaseFenced once another owner or token replaced the lease.
	Fence(ctx context.Context) error
}

// ExecLoopConfig carries the collaborators and limits for one provider-driven run.
type ExecLoopConfig struct {
	Store     *store.Store
	Session   *store.Session
	Streamer  provider.Streamer
	Registry  *tool.Registry
	Allowlist []string
	Pool      *BudgetPool
	// Lease is the lease the caller holds for the run, and is required: the loop never acquires or
	// releases a lease, it fences this one, and the compaction fences it too. The item, owner and
	// token come from it. The caller keeps it held until the post-loop state writers have returned
	// and then releases it; its Item must not be empty.
	Lease      ExecLease
	MaxTurns   int64
	TurnBudget int64
	// MaxTokens caps each provider turn's response tokens; <= 0 uses
	// the model-table default (ResolveMaxOutput).
	MaxTokens int64
	// SystemPrompt, when non-empty, is sent as a message of role system first
	// in every provider request of the loop. It is not persisted as an event
	// and is not part of replayed history.
	SystemPrompt string
	// Compaction is the config's compaction object; its zero value means the ADR-010
	// defaults. It decides the automatic compaction before each provider request.
	Compaction CompactionConfig
	// ProfileWindow is the context_window of the profile the session runs under (a
	// delegated child, the swarm coordinator); 0 for a plain root session.
	ProfileWindow int
	// Hooks is the loaded root hook set (ADR-012 D22); nil is the empty set. Nothing fires it yet.
	Hooks HookSet
}

// compactRun is the run-scoped state of the automatic trigger (ADR-010 "When"): the baseline
// of the context size and whether a compaction has already failed in this run.
type compactRun struct {
	// failed is set by the first failed automatic compaction: it is not retried in the run.
	failed bool
	// base is the baseline in tokens (the last reported turn context, or the last
	// compaction's tokens_after) when hasBase is true; otherwise the whole history is estimated.
	base    int
	hasBase bool
	// appended is the estimate in tokens of what was appended since base.
	appended int
}

// execOverflow is runExecTurn's signal that the provider said the request does not fit the context
// window and the non-terminal marker is written: the loop compacts and retries once.
type execOverflow struct {
	// cause is the provider's own error; it wraps provider.ErrContextOverflow.
	cause error
}

// retryAfter says what the overflow retry follows: a provider error (cause) or, with no cause, a
// length finish.
type retryAfter struct {
	cause error
}

// ExecLoopResult reports one completed bounded run.
type ExecLoopResult struct {
	Turns           int64
	FinalContent    string
	ToolCalls       int64
	BudgetExhausted bool
	// FinishReason is the finish reason of the final provider turn;
	// empty when the provider reported none.
	FinishReason string
	// Usage sums the provider usage of the turns that reported it; a turn
	// without usage adds nothing (Usage.UsageTurns tells how many did).
	Usage UsageTotals
	// LastContextTokens is ContextTokens of the last turn that reported
	// usage; meaningful only when LastContextReported is true.
	LastContextTokens int64
	// LastContextReported is false when no turn reported usage.
	LastContextReported bool
}

// execTurn is one provider turn's streamed outcome inside the loop.
type execTurn struct {
	// Content is the concatenated assistant text.
	Content string
	// Calls are the complete tool calls the turn requested.
	Calls []provider.ToolCall
	// FinishReason is the neutral reason of the turn's finish event;
	// empty when the provider sent none.
	FinishReason string
	// Usage is the usage of the turn's finish event; nil when not reported.
	Usage *provider.Usage
}

// SessionExecLoopRequest carries the CLI-parsed arguments for one
// provider-driven loop run.
type SessionExecLoopRequest struct {
	// Allow lists inline allowlisted tool names.
	Allow []string
	// AllowFile is the --allow-file manifest path.
	AllowFile string
	// RequireAllowlist makes an empty loaded allowlist (inline plus
	// manifest) a usage error instead of a tool-less loop run.
	RequireAllowlist bool
	// Workdir is the --workdir override; empty defaults beside the db.
	Workdir string
	// Agents is the --agents scope (user, project or both); empty offers no
	// delegation. A scope requires `delegate` on the allowlist and the other
	// way round; both are usage errors.
	Agents string
	// ProjectRoot is where project profiles are found when Workdir is empty;
	// empty means the process current directory. The CLI leaves it empty; it is
	// a seam for in-process callers and specs, which must not change directory.
	ProjectRoot string
	// HooksScope is the already-parsed --hooks scope (ADR-012). The zero value
	// means not set: hooks are not loaded yet, and an unset scope must never load
	// the user hook file (fail closed); the CLI applies its default, user, itself.
	// It is carried but not yet acted on: the loader wiring is the follow-up, which
	// finds the project file under HooksProjectRoot(req), never under Workdir.
	HooksScope HookScope
	// Budget is the total loop budget pool; <= 0 means unlimited turns.
	Budget int64
	// MaxTokens caps each provider response; <= 0 uses the model-table default (ResolveMaxOutput).
	MaxTokens int64
	// MaxTurns bounds the loop; <= 0 uses execDefaultMaxTurns (25).
	MaxTurns int64
	// Compaction is the config's compaction object, applied to the run's sessions.
	Compaction CompactionConfig
	// Lease is the exec lease the caller holds for the run, and is required; RunSessionExecLoop
	// forwards it to the loop, which neither acquires nor releases it. Its Item must be
	// "exec:"+Session.ID. The caller releases it after RunSessionExecLoop has returned, that is
	// after the post-loop state writers.
	Lease ExecLease
	// Hooks is the root hook set of the run (ADR-012 D22), set on the root's loop and on the
	// root's own delegate tool so every child inherits it; nil is the empty set.
	Hooks HookSet
}

// UsageTotals sums the provider usage of the turns that reported it. A turn
// without usage adds nothing and is not counted in UsageTurns, so a caller can
// tell a partial total from a complete one.
type UsageTotals struct {
	// InputTokens is the summed prompt tokens billed at the full rate.
	InputTokens int64
	// OutputTokens is the summed tokens the model generated.
	OutputTokens int64
	// CacheReadTokens is the summed prompt tokens served from the cache.
	CacheReadTokens int64
	// CacheWriteTokens is the summed prompt tokens written to the cache.
	CacheWriteTokens int64
	// UsageTurns is how many turns reported usage.
	UsageTurns int64
}

// SessionUsage is the usage a session's event log records, as `session doctor`
// reports it. It is computed from the assistant.message events only, so a log
// written before usage was recorded reads as UsageTurns 0: not reported.
type SessionUsage struct {
	// Totals sums the turns that reported usage; Totals.UsageTurns is
	// the turns_reported of the wire shape.
	Totals UsageTotals
	// TurnsTotal counts every assistant.message event, reported or not.
	TurnsTotal int64
	// LastContextTokens is ContextTokens of the last turn that reported
	// usage; it is meaningful only when Totals.UsageTurns > 0.
	LastContextTokens int64
}

const (
	// DefaultOutputCap bounds the default max_tokens of a model whose table
	// maximum is larger (ResolveMaxOutput).
	DefaultOutputCap = 32768
	// DefaultMaxOutput is the max_tokens of a model the output table lacks.
	DefaultMaxOutput int64 = 8192
)

// replayFacts is what the compaction readers need of a whole log to agree with rebuildHistory about
// which events it replays: the ids that have a persisted tool.result, and the seq of each assistant
// message the rebuild drops because a non-terminal output_truncated marker followed it.
type replayFacts struct {
	answered map[string]bool
	dropped  map[int64]bool
}

// HookSet is the loaded root hook set (ADR-012 D22) a run carries down to every run it starts: a
// delegated child, a nested child, a resumed child and a swarm packet child. It is an opaque value
// here: this package only plumbs it, and nothing fires a hook from it yet. The value is copied
// from the parent's config into the child's and never taken from a profile, so a child can
// neither drop nor replace it. The nil HookSet is the empty set, which is what production
// carries until the packet that fires gates; it stays nil all the way down.
//
// Pass an untyped nil for the empty set, never a nil pointer: a nil *T stored in the interface is
// a non-nil HookSet, so `== nil` is false and Digest runs on a nil receiver. A caller that holds
// a possibly-nil pointer normalises it with emptyHookSet. Do not compare two HookSets with ==:
// an implementation may hold a slice, which makes the comparison panic at run time; compare
// Digest values instead.
//
// The loaded-set value of packet 1 (LoadedHooks{Scope, Hooks}, in hooks_types.go) satisfies it
// once the packet that records `hooks.loaded` adds a `Digest() string` method to it.
type HookSet interface {
	// Digest is the hex SHA-256 of the effective set, the value `hooks.loaded` records.
	Digest() string
}
