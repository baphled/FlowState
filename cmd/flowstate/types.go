package main

import "github.com/baphled/flowstate/internal/app"

// cliError is a failure carrying one of the closed error codes; it
// renders as a single JSON object on stderr.
type cliError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// flags holds the parsed global flags for one invocation.
type flags struct {
	db     string
	dbSet  bool // --db was supplied explicitly
	json   bool
	status string
	// session new
	provider   string
	model      string
	task       string
	parent     string
	reserve    int64
	reserveSet bool
	follow     bool
	// session message
	msgFrom      string
	msgBody      string
	manifestPath string
	// config file (CLI-AND-API-CONTRACT §4)
	configPath   string
	config       cliConfig
	configSource string
	// session tool
	tool      string
	toolArg   string
	allow     []string
	allowFile string
	workdir   string
	agents    string        // --agents scope: user, project or both; empty offers no delegation
	hooks     app.HookScope // --hooks scope as parsed; empty means not given, see hooksScope
	hooksSet  bool          // --hooks was supplied explicitly
	// session unlock
	force bool // --force: delete the lease row instead of reporting it
	// session compact
	instructions string // --instructions: a focus line for the summary
	// session run
	budget    int64
	budgetSet bool // --budget was supplied explicitly
	maxTokens int64
	maxTurns  int64 // --max-turns tool-loop turn limit; 0 means the default
}

// cliConfig holds one parsed flowstate.json: session defaults that
// explicit flags strictly override. Secrets are never config-sourced;
// the loader rejects secret-looking keys at any depth.
type cliConfig struct {
	DefaultProvider string   `json:"default_provider"`
	DefaultModel    string   `json:"default_model"`
	DefaultBudget   int64    `json:"default_budget"`
	StoragePath     string   `json:"storage_path"`
	ToolAllowlist   []string `json:"tool_allowlist"`
	// Compaction is the optional compaction object (ADR-010, D7).
	Compaction app.CompactionConfig `json:"compaction"`
}

// usageSentinel satisfies errors.Is against app.ErrUsage while
// formatting a usage message.
type usageSentinel struct{}

// sigintCancelledKey marks a context cancelled by SIGINT, SIGTERM or SIGHUP; its
// presence routes session cancel to the exit-130 contract path.
type sigintCancelledKey struct{}

// unlockResult is the JSON object `session unlock` prints (schema session-unlock).
type unlockResult struct {
	Session        string            `json:"session"`
	Item           string            `json:"item"`
	Outcome        string            `json:"outcome"`
	Holder         *app.UnlockHolder `json:"holder"`
	MachineMatches bool              `json:"machine_matches"`
	LegacyRow      bool              `json:"legacy_row"`
	EventAppended  bool              `json:"event_appended"`
	Message        string            `json:"message"`
}
