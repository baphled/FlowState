package app

// SessionDiagnosis is the read-only diagnosis of one session's
// durable state for the `session doctor` CLI surface (ADR-006
// follow-up): the raw row status, the crash classification, whether
// the event log shows run activity, the pending steering-message
// count and the budget snapshot. Values are a point-in-time
// aggregation only; the doctor never mutates the session, and
// recovery stays in the run preamble.
type SessionDiagnosis struct {
	// SessionID echoes the diagnosed session id.
	SessionID string
	// Status is the raw persisted row status, never remapped.
	Status string
	// Crashed is the crash-recovery predicate ANDed with an active
	// row status: only a session the engine could still resume is
	// reported as crashed.
	Crashed bool
	// RunActivity reports provider/tool traffic in the event log —
	// the same evidence DetectCrashedSession uses to separate a
	// crashed run from a freshly created session.
	RunActivity bool
	// PendingMessages counts undelivered steering messages.
	PendingMessages int
	// Reserved is the outstanding reserved budget total.
	Reserved int64
	// Pool is the declared budget pool anchor, nil without a pool.
	Pool *int64
	// Available is pool minus reserved, nil without a pool.
	Available *int64
	// FinishReason is the finish_reason of the session's LAST turn.end
	// event (PR #358), empty when none was recorded or the payload is
	// unparseable. Informational: it is not gated on the row status.
	FinishReason string
	// Truncated is true exactly when the last turn.end finished with
	// provider.FinishLength (answer cut off at the token limit) and the
	// session is neither failed, cancelled, paused nor crashed.
	Truncated bool
	// Usage is the provider usage the event log records; see SessionUsage.
	Usage SessionUsage
	// Compactions summarises the log's context.compacted events; see
	// CompactionStats.
	Compactions CompactionStats
	// ContextWindow is the window resolved for the session's provider and
	// model (ADR-010, D3); Tokens is 0 when its Source is unknown.
	ContextWindow ContextWindow
	// Warnings lists conditions the operator should fix; empty when none.
	Warnings []string
	// Hooks summarises the log's hook events (ADR-012); nil when the log holds none, so the
	// doctor's output is unchanged for a session that never ran a hook.
	Hooks *HookStats
}

// HookStats is what the event log records about lifecycle hooks (ADR-012,
// packet 4d), read as data: the doctor never re-runs a hook. Ids, points and
// reasons are the untrusted text of the log, stripped of control, format and
// line-separator characters and bounded before they reach either surface.
type HookStats struct {
	// Loaded summarises the LAST hooks.loaded of the log, the set the latest root
	// run applied; nil when none was recorded (a child or a swarm run writes
	// none: ADR-012 known gap 8).
	Loaded *HookLoaded
	// Unmatched lists, in log order, the hook.invoked events no hook.result
	// followed: a crash or a cancellation between the two (ADR-012 D12). The hook
	// may have had side effects, and a resume does not re-run it.
	Unmatched []HookInvocation
	// Denials lists, per hook that returned deny, its point, the count and the
	// last reason recorded. A fail-closed failure that denied a call is counted
	// in Failed, not here.
	Denials []HookDenial
	// Failed counts the hook.result events whose decision is failed.
	Failed int64
	// Outcome is the last stop (turn.after) or refusal (user.prompt) a
	// hook.result records; nil when the log holds neither.
	Outcome *HookOutcome
}

// HookLoaded is the hooks.loaded summary the doctor reports.
type HookLoaded struct {
	// Scope is the --hooks scope the set was loaded under.
	Scope string
	// SetSHA256 is the digest of the effective set, the composition pin.
	SetSHA256 string
	// IDs groups the hook ids by their source (user, project, a profile).
	IDs map[string][]string
	// Skipped lists the profile hooks the scope left out, with the reason.
	Skipped []HookSkipped
	// Inert lists the hooks whose point the run's path lacks.
	Inert []string
}

// HookSkipped is one hooks.loaded skipped entry: a hook id and why it was skipped.
type HookSkipped struct {
	ID     string
	Reason string
}

// HookInvocation is one hook.invoked the log holds without a result.
type HookInvocation struct {
	Point string
	ID    string
	// Seq is the invocation's event sequence, so `session events` finds it.
	Seq int64
}

// HookDenial is one hook's denials: the count of its deny results and the last reason.
type HookDenial struct {
	Point  string
	ID     string
	Count  int64
	Reason string
}

// HookOutcome is the last stop or refusal a hook recorded: Code is
// CodeHookStop or CodeHookRefused, ID the hook, Reason the refusal reason (a
// stop records none; a fail-closed user.prompt failure's is its failure code).
type HookOutcome struct {
	Code   string
	ID     string
	Reason string
}

// The types below serve the context window the doctor reports and ResolveContextWindow
// (contextwindow.go) resolves; they live here to keep the packet inside its file budget.
// Context-window sources reported by ResolveContextWindow and the session
// doctor (ADR-010, D3).
const (
	// WindowSourceProfile marks a window taken from an agent profile.
	WindowSourceProfile = "profile"
	// WindowSourceConfig marks a window taken from compaction.context_window.
	WindowSourceConfig = "config"
	// WindowSourceTable marks a window taken from the registry table.
	WindowSourceTable = "table"
	// WindowSourceUnknown marks a model whose window no source gives.
	WindowSourceUnknown = "unknown"
)

// Compaction defaults (ADR-010, D7). They are applied in one place,
// CompactionConfig.Resolved.
const (
	// DefaultCompactionReserve is the default compaction.reserve in tokens.
	DefaultCompactionReserve = 16384
	// DefaultCompactionKeepRecent is the default compaction.keep_recent in tokens.
	DefaultCompactionKeepRecent = 20000
)

// CompactionConfig is the `compaction` object of the config file as written:
// a nil field is absent and takes its default from Resolved. No key contains
// "token", "key", "secret" or "password", which the config's secret-key filter
// would reject (ADR-005); the values are all counts of tokens or a switch.
type CompactionConfig struct {
	// Enabled switches automatic compaction; absent means true.
	Enabled *bool `json:"enabled"`
	// Reserve is the tokens kept free for the next response; absent means
	// DefaultCompactionReserve.
	Reserve *int `json:"reserve"`
	// KeepRecent is the estimated tokens of recent history kept verbatim;
	// absent means DefaultCompactionKeepRecent.
	KeepRecent *int `json:"keep_recent"`
	// ContextWindow is the window in tokens for every model; absent means the
	// registry table decides.
	ContextWindow *int `json:"context_window"`
}

// CompactionSettings is CompactionConfig with every default applied.
type CompactionSettings struct {
	// Enabled reports whether automatic compaction is on.
	Enabled bool
	// Reserve is the tokens kept free for the next response.
	Reserve int
	// KeepRecent is the estimated tokens of recent history kept verbatim.
	KeepRecent int
	// ContextWindow is the configured window; 0 means none is configured.
	ContextWindow int
}

// ContextWindow is a resolved context window. Tokens is 0 exactly when Source
// is WindowSourceUnknown.
type ContextWindow struct {
	// Tokens is the window size in tokens, 0 when unknown.
	Tokens int
	// Source is one of the WindowSource constants.
	Source string
}
