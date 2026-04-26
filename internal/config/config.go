// Package config loads FlowState application configuration.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	pluginpkg "github.com/baphled/flowstate/internal/plugin"
	"gopkg.in/yaml.v3"
)

// Config aliases AppConfig for callers that use the shorter configuration name.
type Config = AppConfig

// AppConfig holds the complete application configuration.
type AppConfig struct {
	Providers ProvidersConfig `json:"providers" yaml:"providers"`
	AgentDir  string          `json:"agent_dir" yaml:"agent_dir"`
	// AgentDirs lists user-defined agent directories merged into the registry after
	// AgentDir. Agents in later directories override agents with the same ID from
	// earlier directories and from AgentDir. Tilde paths (~/...) are expanded at load time.
	AgentDirs          []string                         `json:"agent_dirs" yaml:"agent_dirs"`
	SkillDir           string                           `json:"skill_dir" yaml:"skill_dir"`
	DataDir            string                           `json:"data_dir" yaml:"data_dir"`
	LogLevel           string                           `json:"log_level" yaml:"log_level"`
	DefaultAgent       string                           `json:"default_agent" yaml:"default_agent"`
	CategoryRouting    map[string]engine.CategoryConfig `json:"category_routing" yaml:"category_routing"`
	Plugins            PluginsConfig                    `json:"plugins" yaml:"plugins,omitempty"`
	MCPServers         []MCPServerConfig                `yaml:"mcp_servers,omitempty"`
	AlwaysActiveSkills []string                         `yaml:"always_active_skills,omitempty"`
	Harness            HarnessConfig                    `json:"harness" yaml:"harness"`
	AgentOverrides     map[string]AgentOverrideConfig   `json:"agent_overrides" yaml:"agent_overrides"`
	// ContextAssemblyHooks lets callers inject custom context assembly hooks at runtime.
	ContextAssemblyHooks []pluginpkg.ContextAssemblyHook `json:"-" yaml:"-"`
	SessionRecording     bool                            `json:"session_recording" yaml:"session_recording"`
	// SessionRecordingDir overrides the filesystem location the
	// session recorder writes to. When empty the effective directory
	// is derived from the active sessions directory (--sessions-dir
	// or cfg.DataDir/sessions) via `<sessionsDir>/recordings`. When
	// that derivation is unavailable the recorder falls back to the
	// user cache dir. Expressed as a top-level YAML key rather than
	// a nested session_recording block so the existing boolean
	// `session_recording` remains a non-breaking literal.
	SessionRecordingDir string       `json:"session_recording_dir" yaml:"session_recording_dir"`
	Qdrant              QdrantConfig `json:"qdrant" yaml:"qdrant"`
	VaultPath           string       `json:"vault_path" yaml:"vault_path"`
	// Compression controls the three-layer context compression system
	// (micro-compaction, auto-compaction, session-memory). All layers
	// default to disabled; see internal/context.DefaultCompressionConfig.
	Compression contextpkg.CompressionConfig `json:"compression" yaml:"compression"`

	// StreamTimeout overrides the per-LLM-stream wall-clock budget. Empty
	// means inherit the engine's compiled-in default (5m). Long delegations
	// on slow providers (e.g. zai/glm-4.7) are the typical reason to raise
	// this. Format: a Go duration string ("15m", "300s").
	StreamTimeout string `json:"stream_timeout,omitempty" yaml:"stream_timeout,omitempty"`
	// ToolTimeout overrides the per-tool-call wall-clock budget. Empty
	// means inherit the engine's compiled-in default (2m). The delegate
	// tool already opts out of this via TimeoutOverrider, so raising it
	// affects shell-style tools (bash, read, web) only.
	ToolTimeout string `json:"tool_timeout,omitempty" yaml:"tool_timeout,omitempty"`
	// BackgroundOutputTimeout overrides the default poll-until-complete
	// budget on the background_output tool when the model does not pass
	// an explicit `timeout` argument. Empty means inherit the compiled-in
	// default (120s).
	BackgroundOutputTimeout string `json:"background_output_timeout,omitempty" yaml:"background_output_timeout,omitempty"`

	// PlanLocation overrides the directory FlowState reads/writes plan
	// markdown files from. Resolution rules (see ResolvedPlanLocation):
	//
	//   - Empty (default): walk up from the current working directory
	//     looking for a `.flowstate/` marker directory. If found, use
	//     `<projectRoot>/.flowstate/plans/`. If no marker is found, fall
	//     back to `${cfg.DataDir}/plans/` so users without a project
	//     setup still get a working location.
	//   - Non-empty: the literal path is used verbatim, with `~` and
	//     `~/` expanded against the user's home directory. Bare relative
	//     paths are resolved against the user's CWD at call time, NOT
	//     against `cfg.DataDir`. Allows `plan_location: ~/work/shared-plans/`
	//     for a global override or `plan_location: ./.flowstate/plans/`
	//     for a project-local pin.
	//
	// The project-marker default mirrors OMO's pattern: plans live next
	// to the code they describe and can be checked into version control
	// alongside it.
	PlanLocation string `json:"plan_location,omitempty" yaml:"plan_location,omitempty"`

	// EmbeddingModel names the model used for vector-search embeddings
	// across the application (recall queries, knowledge distillation,
	// per-agent context_management defaults). It is deliberately separated
	// from the chat-provider model surface for three reasons:
	//
	//  1. It powers vector search, not user-facing inference. Swapping it
	//     does not change reasoning quality — it changes the SHAPE of the
	//     vectors stored in Qdrant.
	//  2. The model MUST be consistent across an entire vector-store
	//     deployment. Vectors produced by different embedding models are
	//     not comparable; mixing them silently corrupts recall.
	//  3. A multi-worker / multi-pod cluster wants every node producing
	//     vectors that index into the same collection. Centralising the
	//     embedding model in one config knob lets a cluster operator pin
	//     it cluster-wide while individual agents still customise their
	//     chat-provider choice freely.
	//
	// Empty means "use the historical default `nomic-embed-text`" — an
	// Ollama-served 768-dim Cosine model that matches the existing
	// flowstate Qdrant collection shape.
	EmbeddingModel string `json:"embedding_model,omitempty" yaml:"embedding_model,omitempty"`

	// ToolCapableModels lists model-name patterns whose underlying provider
	// is known to reliably emit structured tool calls. Delegation consults
	// this list before spawning a sub-agent: when the resolved (provider,
	// model) does not match any entry here (and is not on
	// ToolIncapableModels), the delegate tool fails closed with a
	// structured error instead of streaming a sub-agent that would
	// silently produce zero tool calls. See KB:
	//   - Investigations/GLM Delegation Failure After Rebuild (April 2026).md
	//   - Investigations/Non-Anthropic Provider Stream Termination Investigation (April 2026).md
	//   - Bug Fixes/Planner Harness Rescue - April 2026.md
	//
	// Patterns use a prefix match with a single `*` glob suffix:
	//   - `claude-*` matches every Anthropic Claude model.
	//   - `qwen3:*` matches `qwen3:8b`, `qwen3:14b`, `qwen3:30b-a3b`.
	//   - `gpt-oss:20b` is a literal match (the `-4k`/`-8k` clones are
	//     deliberately NOT covered because they are context-clamped and
	//     have shown different tool-call reliability in practice).
	//
	// Empty (and nil) means "fail closed" — no model is considered tool
	// capable. Operators opt-in by listing patterns here. Defaults come
	// from DefaultConfig (the known-good shortlist documented in the
	// FlowState README).
	ToolCapableModels []string `json:"tool_capable_models,omitempty" yaml:"tool_capable_models,omitempty"`

	// ToolIncapableModels lists model-name patterns whose underlying
	// provider is known NOT to emit reliable structured tool calls. This
	// list takes precedence over ToolCapableModels: a model that matches
	// any pattern here is rejected regardless of the allow list. Same
	// glob-suffix matching as ToolCapableModels.
	//
	// Defaults pin the four models documented in the KB notes above as
	// silently producing zero tool calls under FlowState's current
	// prompts/templates: `llama3.2*`, `qwen2.5-coder*`, `glm-4.7`, and
	// `mistral:7b`.
	ToolIncapableModels []string `json:"tool_incapable_models,omitempty" yaml:"tool_incapable_models,omitempty"`

	// SystemPromptBudget overrides the model-context fallback used when
	// the failover manager and token counter cannot supply a concrete
	// context length for the active provider/model. Zero (default) lets
	// the engine inherit ctxstore.DefaultModelContextFallback (16K).
	// Operators with hardware that warrants a different cap pin the
	// fallback per-deployment via this knob; the env var
	// FLOWSTATE_SYSTEM_PROMPT_BUDGET takes precedence at load time.
	//
	// Why this exists: prior to this knob the fallback was a hardcoded
	// 4096 that quietly truncated ~70% of an 11-skill FlowState system
	// prompt to fit. The new default plus this override path lets every
	// provider in the support matrix (Anthropic 200K, OpenAI/Copilot/
	// Gemini 128K, ZAI/OpenZen 128K+, Ollama qwen3/llama3.1/devstral
	// 32K-128K) carry the full prompt without losing skill content.
	SystemPromptBudget int `json:"system_prompt_budget,omitempty" yaml:"system_prompt_budget,omitempty"`
}

// ParsedStreamTimeout returns the parsed value of StreamTimeout, or 0 when
// unset/invalid (callers treat 0 as "use engine default"). Invalid input is
// logged once at WARN and treated as zero so a typo never crashes startup.
// A nil receiver returns 0 — App test fixtures construct App with Config=nil.
func (c *AppConfig) ParsedStreamTimeout() time.Duration {
	if c == nil {
		return 0
	}
	return parseDurationField(c.StreamTimeout, "stream_timeout")
}

// ParsedToolTimeout returns the parsed value of ToolTimeout (see
// ParsedStreamTimeout for semantics, including nil-receiver behaviour).
func (c *AppConfig) ParsedToolTimeout() time.Duration {
	if c == nil {
		return 0
	}
	return parseDurationField(c.ToolTimeout, "tool_timeout")
}

// ParsedBackgroundOutputTimeout returns the parsed value of
// BackgroundOutputTimeout (see ParsedStreamTimeout for semantics, including
// nil-receiver behaviour).
func (c *AppConfig) ParsedBackgroundOutputTimeout() time.Duration {
	if c == nil {
		return 0
	}
	return parseDurationField(c.BackgroundOutputTimeout, "background_output_timeout")
}

// SystemPromptBudgetEnv is the environment variable operators set to
// override AppConfig.SystemPromptBudget without editing config.yaml.
// The env wins over the YAML field (matching the existing OPENAI_API_KEY
// / ANTHROPIC_API_KEY precedence in resolveProviderKey).
const SystemPromptBudgetEnv = "FLOWSTATE_SYSTEM_PROMPT_BUDGET"

// ResolvedSystemPromptBudget returns the effective system-prompt budget
// in tokens, applying the documented precedence: the env var overrides
// the YAML field, and zero from both means "inherit the engine default"
// (ctxstore.DefaultModelContextFallback). Invalid env values are
// logged once at WARN and treated as unset so a typo cannot silently
// reintroduce the legacy 4096 truncation.
//
// Returns:
//   - The override token cap when one is set; zero when neither the
//     env var nor the YAML field carries a positive value.
//
// Side effects:
//   - Reads the FLOWSTATE_SYSTEM_PROMPT_BUDGET environment variable.
//   - Logs a single WARN slog line when the env value fails to parse.
func (c *AppConfig) ResolvedSystemPromptBudget() int {
	if v := os.Getenv(SystemPromptBudgetEnv); v != "" {
		if parsed, err := parsePositiveInt(v); err == nil {
			return parsed
		} else {
			slog.Warn("config: invalid env value; falling back to config / engine default",
				"key", SystemPromptBudgetEnv, "value", v, "error", err)
		}
	}
	if c == nil {
		return 0
	}
	if c.SystemPromptBudget > 0 {
		return c.SystemPromptBudget
	}
	return 0
}

// parsePositiveInt parses a token-budget string. Returns an error for
// non-numeric input or non-positive values so callers can surface the
// problem without falling silently back to defaults.
//
// Expected:
//   - s is a candidate integer string.
//
// Returns:
//   - The parsed positive integer on success.
//   - An error when s is empty, non-numeric, or <= 0.
//
// Side effects:
//   - None.
func parsePositiveInt(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty value")
	}
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("non-numeric character %q", ch)
		}
		n = n*10 + int(ch-'0')
	}
	if n <= 0 {
		return 0, fmt.Errorf("value must be positive")
	}
	return n, nil
}

// ResolvedPlanLocation returns the directory FlowState should use for plan
// markdown files. The three-tier resolution mirrors the field godoc on
// PlanLocation:
//
//  1. If PlanLocation is non-empty, expand a leading `~` / `~/` against
//     the user's home directory and return the result. Bare relative
//     paths are kept relative — they are resolved against the user's
//     CWD at call time, never against cfg.DataDir.
//  2. Otherwise walk parents of the current working directory looking
//     for a `.flowstate/` marker. The first match wins; the resolver
//     returns `<dir>/.flowstate/plans/`. This matches OMO's project-
//     local layout and allows shared plans via `git`.
//  3. Otherwise fall back to `<DataDir>/plans/` so fresh users with no
//     project marker still get a working location.
//
// A nil receiver returns the empty string. App test fixtures construct
// App with Config=nil and exercise paths that consult this helper.
func (c *AppConfig) ResolvedPlanLocation() string {
	if c == nil {
		return ""
	}
	if c.PlanLocation != "" {
		return expandTilde(c.PlanLocation)
	}
	if dir := findProjectFlowstateDir(); dir != "" {
		return filepath.Join(dir, "plans")
	}
	return filepath.Join(c.DataDir, "plans")
}

// findProjectFlowstateDir walks parents of the current working directory
// looking for a `.flowstate/` directory. Returns the absolute path to the
// marker directory itself (so callers can append `plans/` etc.), or the
// empty string when no marker is found.
//
// Side effects:
//   - Reads os.Getwd() and stat()s candidate parents.
func findProjectFlowstateDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir := cwd
	for {
		candidate := filepath.Join(dir, ".flowstate")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func parseDurationField(s, key string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		slog.Warn("config: invalid duration string; falling back to default",
			"key", key, "value", s, "error", err)
		return 0
	}
	return d
}

// DefaultEmbeddingModel is the historical embedding model used when
// AppConfig.EmbeddingModel is empty. See AppConfig.EmbeddingModel for
// the rationale around centralising this knob.
const DefaultEmbeddingModel = "nomic-embed-text"

// ResolvedEmbeddingModel returns the embedding model to use for vector-store
// operations: the explicit cfg.EmbeddingModel when set, otherwise the
// historical default `nomic-embed-text`. A nil receiver returns the default
// so test fixtures that construct App with Config=nil still produce
// well-formed vectors.
func (c *AppConfig) ResolvedEmbeddingModel() string {
	if c == nil || c.EmbeddingModel == "" {
		return DefaultEmbeddingModel
	}
	return c.EmbeddingModel
}

// DefaultProviderModel returns the chat model configured for the provider
// named in cfg.Providers.Default. Returns empty when the default provider
// has no model configured (callers treat empty as "no usable default" and
// surface their own error).
func (c *AppConfig) DefaultProviderModel() string {
	if c == nil {
		return ""
	}
	switch c.Providers.Default {
	case "anthropic":
		return c.Providers.Anthropic.Model
	case "openai":
		return c.Providers.OpenAI.Model
	case "ollama":
		return c.Providers.Ollama.Model
	case "github", "github-copilot":
		return c.Providers.GitHub.Model
	case "zai":
		return c.Providers.ZAI.Model
	case "openzen":
		return c.Providers.OpenZen.Model
	default:
		return ""
	}
}

// QdrantConfig provides configuration for Qdrant-based recall storage.
//
// Fields:
//   - URL: The base URL of the Qdrant server (e.g., "http://localhost:6333").
//   - Collection: The Qdrant collection name to use for recall storage.
//   - APIKey: The optional API key for authenticated Qdrant instances.
//
// Expected:
//   - Used to configure Qdrant-backed recall in the application engine.
//
// Returns:
//   - None.
//
// Side effects:
//   - None.
type QdrantConfig struct {
	URL        string `json:"url" yaml:"url"`
	Collection string `json:"collection" yaml:"collection"`
	APIKey     string `json:"api_key" yaml:"api_key"`
}

// ProvidersConfig configures all available LLM providers.
type ProvidersConfig struct {
	Anthropic ProviderConfig `json:"anthropic" yaml:"anthropic"`
	Default   string         `json:"default" yaml:"default"`
	GitHub    ProviderConfig `json:"github" yaml:"github"`
	Ollama    ProviderConfig `json:"ollama" yaml:"ollama"`
	OpenAI    ProviderConfig `json:"openai" yaml:"openai"`
	OpenZen   ProviderConfig `json:"openzen" yaml:"openzen"`
	ZAI       ProviderConfig `json:"zai" yaml:"zai"`
}

// ProviderConfig holds configuration for a single LLM provider.
type ProviderConfig struct {
	Host   string      `json:"host" yaml:"host"`
	APIKey string      `json:"api_key" yaml:"api_key"`
	Model  string      `json:"model" yaml:"model"`
	OAuth  OAuthConfig `json:"oauth" yaml:"oauth"`
}

// OAuthConfig holds OAuth-specific configuration for a provider.
type OAuthConfig struct {
	Enabled   bool   `json:"enabled" yaml:"enabled"`
	ClientID  string `json:"client_id" yaml:"client_id"`
	TokenFile string `json:"token_file" yaml:"token_file"`
	Scopes    string `json:"scopes" yaml:"scopes"`
	UseOAuth  bool   `json:"use_oauth" yaml:"use_oauth"`
}

// MCPToolPermission defines the permission mode for a specific MCP server tool.
type MCPToolPermission struct {
	ServerName string `yaml:"server_name"`
	ToolName   string `yaml:"tool_name"`
	Permission string `yaml:"permission"`
}

// MCPServerConfig holds configuration for a single MCP server connection.
// Name and Command are required fields.
type MCPServerConfig struct {
	Name    string            `yaml:"name"`
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
	Enabled bool              `yaml:"enabled"`
}

// HarnessConfig holds configuration for the planning harness.
//
// Each field controls an optional layer of the harness. By default,
// the harness is enabled but the critic and voting are disabled.
// MaxRetries controls how many evaluation attempts the harness makes
// before returning a best-effort result; defaults to 1.
//
// Mode selects the harness loop type. Valid values are "plan" (default)
// and "execution". When empty, "plan" behaviour is assumed.
//
// LearningEnabled enables the async learning loop for this harness.
// LearningOnFailure triggers learning captures when evaluation fails.
// LearningOnNovelty triggers learning captures when novel output is detected.
//
// CriticModel overrides the chat model used by the LLM critic. When empty,
// the harness falls back to the default provider's primary model (resolved
// from cfg.Providers.<default>.Model). Set this only when the critic should
// run on a different model from the agent under critique — e.g. a cheaper
// reviewer over an expensive primary, or vice versa.
type HarnessConfig struct {
	Enabled            bool   `json:"enabled" yaml:"enabled"`
	ProjectRoot        string `json:"project_root" yaml:"project_root"`
	CriticEnabled      bool   `json:"critic_enabled" yaml:"critic_enabled"`
	CriticModel        string `json:"critic_model,omitempty" yaml:"critic_model,omitempty"`
	VotingEnabled      bool   `json:"voting_enabled" yaml:"voting_enabled"`
	IncrementalEnabled bool   `json:"incremental_enabled" yaml:"incremental_enabled"`
	MaxRetries         int    `json:"harness_max_retries" yaml:"harness_max_retries"`
	Mode               string `json:"mode" yaml:"mode"`
	LearningEnabled    bool   `json:"learning_enabled" yaml:"learning_enabled"`
	LearningOnFailure  bool   `json:"learning_on_failure" yaml:"learning_on_failure"`
	LearningOnNovelty  bool   `json:"learning_on_novelty" yaml:"learning_on_novelty"`
}

// AgentOverrideConfig holds per-agent configuration overrides.
//
// PromptAppend contains text to be appended to an agent's system prompt
// at runtime, without modifying the agent .md file.
type AgentOverrideConfig struct {
	PromptAppend string `json:"prompt_append" yaml:"prompt_append"`
}

// PluginsConfig holds configuration for FlowState plugins.
type PluginsConfig struct {
	Dir      string         `json:"dir" yaml:"dir,omitempty"`
	Enabled  []string       `json:"enabled" yaml:"enabled,omitempty"`
	Disabled []string       `json:"disabled" yaml:"disabled,omitempty"`
	Timeout  int            `json:"timeout" yaml:"timeout,omitempty"`
	Failover FailoverConfig `json:"failover" yaml:"failover,omitempty"`
}

// FailoverConfig holds configurable tier mappings for provider failover.
type FailoverConfig struct {
	Tiers map[string]string `json:"tiers" yaml:"tiers,omitempty"`
}

// Dir returns the configuration directory path.
//
// Checks XDG_CONFIG_HOME environment variable first, then falls back to
// ~/.config/flowstate. Returns the directory path (not the config file).
//
// Returns:
//   - The path to the FlowState configuration directory.
//
// Side effects:
//   - None.
func Dir() string {
	if xdgConfigHome := os.Getenv("XDG_CONFIG_HOME"); xdgConfigHome != "" {
		return filepath.Join(xdgConfigHome, "flowstate")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "flowstate")
	}
	return filepath.Join(homeDir, ".config", "flowstate")
}

// DataDir returns the data directory path.
//
// Checks XDG_DATA_HOME environment variable first, then falls back to
// ~/.local/share/flowstate.
//
// Returns:
//   - The path to the FlowState data directory.
//
// Side effects:
//   - None.
func DataDir() string {
	if xdgDataHome := os.Getenv("XDG_DATA_HOME"); xdgDataHome != "" {
		return filepath.Join(xdgDataHome, "flowstate")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share", "flowstate")
	}
	return filepath.Join(homeDir, ".local", "share", "flowstate")
}

// DefaultConfig returns sensible default configuration values.
//
// AgentDir and SkillDir live under XDG_CONFIG (Dir()) rather than XDG_DATA
// because agent manifests and skill bundles are user-edited configuration —
// adding `harness.critic_enabled` to `plan-writer.md`, editing `SKILL.md`,
// swapping a model name, etc. — and XDG_CONFIG is the canonical home for
// that class of file. Swarms (also user-edited) follow the same rule.
// Cache-style outputs (sessions, plans) stay under DataDir because they
// are derived state, not user input.
//
// Returns:
//   - An AppConfig populated with default provider and directory settings.
//
// Side effects:
//   - Resolves the user home directory to set the data path.
func DefaultConfig() *AppConfig {
	dataDir := DataDir()

	return &AppConfig{
		Providers: ProvidersConfig{
			Default: "anthropic",
			Ollama: ProviderConfig{
				Host:  "http://localhost:11434",
				Model: "llama3.2",
			},
			OpenAI: ProviderConfig{
				Model: "gpt-4o",
			},
			Anthropic: ProviderConfig{
				Model: "claude-sonnet-4-20250514",
			},
		},
		AgentDir:        filepath.Join(Dir(), "agents"),
		SkillDir:        filepath.Join(Dir(), "skills"),
		DataDir:         dataDir,
		LogLevel:        "info",
		DefaultAgent:    "executor",
		CategoryRouting: engine.DefaultCategoryRouting(),
		AlwaysActiveSkills: []string{
			"pre-action",
			"memory-keeper",
			"token-cost-estimation",
			"retrospective",
			"note-taking",
			"knowledge-base",
			"discipline",
			"skill-discovery",
			"agent-discovery",
		},
		Harness: HarnessConfig{
			Enabled:            true,
			CriticEnabled:      false,
			VotingEnabled:      false,
			IncrementalEnabled: false,
			MaxRetries:         1,
		},
		Plugins: PluginsConfig{
			Failover: FailoverConfig{
				Tiers: map[string]string{
					"claude-sonnet-4-20250514":   "tier-0",
					"claude-3-5-sonnet-20241022": "tier-0",
					"gpt-4o":                     "tier-1",
					"gpt-4o-mini":                "tier-2",
					"llama3.2":                   "tier-3",
					"llama3":                     "tier-3",
				},
			},
		},
		AgentOverrides:      make(map[string]AgentOverrideConfig),
		Compression:         contextpkg.DefaultCompressionConfig(),
		ToolCapableModels:   defaultToolCapableModels(),
		ToolIncapableModels: defaultToolIncapableModels(),
	}
}

// defaultToolCapableModels returns the curated shortlist of model-name
// patterns FlowState ships with. These are the models we have direct
// production evidence (citation-backed in the KB's "Local Model Matrix"
// note) of emitting structured tool calls reliably under the current
// FlowState system prompt + provider templates. Operators can extend
// the list via cfg.ToolCapableModels in config.yaml.
//
// Evidence summary for the local-model entries:
//   - qwen3:*       — BFCL-v3 72.4 + RULER 99.2 @32K (Qwen3-30B-A3B-Thinking
//                     model card); qwen3:14b RULER 96.1 @32K (NVIDIA RULER);
//                     qwen3:8b verified live in FlowState (2 tool calls on the
//                     /tmp .md count test).
//   - devstral:latest — SWE-Bench Verified 53.6 (Mistral / Devstral release);
//                       verified live in FlowState (1 tool call after ~120s
//                       first load).
//   - llama3.1:latest — BFCL 0.761 + RULER 87.4 @32K (llm-stats, NVIDIA);
//                       verified live in FlowState.
//   - llama3.3:latest — v2 BFCL ~77 (Galileo); reasoning > FC. Untested live
//                       (70B too slow for the consumer-GPU smoke runner).
//   - gemini-3*     — gemini-3-flash-preview verified live in FlowState
//                     (via github-copilot proxy).
//   - grok-code-*   — grok-code-fast-1 verified live in FlowState (via
//                     github-copilot proxy).
func defaultToolCapableModels() []string {
	return []string{
		"claude-*",
		"gpt-4*",
		"gpt-5*",
		"o1*",
		"o3*",
		"gemini-3*",
		"grok-code-*",
		"qwen3:*",
		"devstral:latest",
		"llama3.1:latest",
		"llama3.3:latest",
	}
}

// defaultToolIncapableModels returns the curated deny list of models
// known (per the KB investigations cited on AppConfig.ToolCapableModels
// and the "Local Model Matrix" note) to either silently emit zero tool
// calls or to trip Ollama-template bugs that intersect FlowState's
// 4–12-tool-calls-per-turn pattern. Deny-list match takes precedence
// over allow-list match, so a future operator who allows
// `qwen2.5-coder:14b` via ToolCapableModels still trips this guard
// until they explicitly remove the deny entry.
//
// Evidence summary:
//   - llama3.2*        — KB: planner produced zero tool calls when failover
//                        landed here (Bug Fixes / Planner Harness Rescue).
//   - qwen2.5-coder*   — KB: broken on clean input under current Ollama
//                        template (Investigations / Non-Anthropic Provider
//                        Stream Termination).
//   - glm-4.7          — KB: returns prose instead of structured tool calls
//                        (Investigations / GLM Delegation Failure).
//   - mistral:7b       — RULER 75.4 @32K → 13.8 @128K (effective ctx ≪32K);
//                        pre-tool-format generation, FC is a prompt hack.
//   - gpt-oss:20b*     — Five open Ollama bugs: no parallel tool calls
//                        (#12159), thinking leaks into tool calls (#12203),
//                        malformed tool names (#11704), 500 on call (#11800),
//                        RAM blowup (#13401). Intersects FlowState's
//                        multi-tool-per-turn pattern hard.
//   - deepseek-r1:*    — Ollama template-broken for tools per #10935 / #8517.
//                        The community fork lucasmg/...-tool-true exists but
//                        has no published bench — local-bench before adopting.
//   - claude-haiku*    — verified live in FlowState: claude-haiku-4.5 returns
//                        prose instead of structured tool calls on the /tmp
//                        .md count test. The cost-optimised distillates trade
//                        tool-call reliability for latency.
//   - gpt-*-mini       — same pattern as claude-haiku: gpt-5-mini verified
//                        prose-only on the same test. The deny pattern uses
//                        the suffix glob so future *-mini releases are
//                        captured automatically.
func defaultToolIncapableModels() []string {
	return []string{
		"llama3.2*",
		"qwen2.5-coder*",
		"glm-4.7",
		"mistral:7b",
		"gpt-oss:20b*",
		"deepseek-r1:*",
		"claude-haiku*",
		"gpt-*-mini",
	}
}

// LoadConfig loads configuration from the default location.
//
// Checks paths in order:
//  1. $XDG_CONFIG_HOME/flowstate/config.yaml
//  2. ~/.config/flowstate/config.yaml
//  3. ~/.flowstate/config.yaml (backwards compatibility)
//
// Returns:
//   - An AppConfig loaded from the first found file, or defaults if none exist.
//   - An error only if a file exists but cannot be parsed.
//
// Side effects:
//   - Reads the configuration file from disk if it exists.
func LoadConfig() (*AppConfig, error) {
	paths := []string{
		filepath.Join(Dir(), "config.yaml"),
		filepath.Join(homeDir(), ".config", "flowstate", "config.yaml"),
		filepath.Join(homeDir(), ".flowstate", "config.yaml"),
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return LoadConfigFromPath(path)
		}
	}

	return DefaultConfig(), nil
}

// homeDir returns the user's home directory, or "." if it cannot be resolved.
//
// Returns:
//   - The user's home directory path, or "." as fallback.
//
// Side effects:
//   - None.
func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

// LoadConfigFromPath loads configuration from the specified file path.
//
// Expected:
//   - path is a file path to a YAML configuration file.
//
// Returns:
//   - An AppConfig loaded from the file, with defaults applied for missing fields.
//   - An error if the file cannot be read or parsed.
//
// Side effects:
//   - Reads the configuration file from disk.
func LoadConfigFromPath(path string) (*AppConfig, error) {
	cleanPath := filepath.Clean(path)
	if _, err := os.Stat(cleanPath); err != nil {
		if os.IsNotExist(err) {
			cfg := DefaultConfig()
			expandPaths(cfg)
			return cfg, nil
		}
		return nil, fmt.Errorf("stat config file %q: %w", cleanPath, err)
	}

	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("reading config file %q: %w", cleanPath, err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %q: %w", cleanPath, err)
	}

	applyDefaults(cfg)
	expandPaths(cfg)
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("validating config file %q: %w", cleanPath, err)
	}
	return cfg, nil
}

// validateConfig runs post-load invariants over the fully-defaulted
// configuration. It is the single choke-point for cross-field rules
// that cannot be encoded in struct tags or default-fill logic.
//
// Expected:
//   - cfg has had applyDefaults and expandPaths run against it.
//
// Returns:
//   - nil when every rule holds.
//   - The first rule violation with enough context for the operator
//     to identify the offending YAML key.
//
// Side effects:
//   - None.
func validateConfig(cfg *AppConfig) error {
	if err := cfg.Compression.Validate(); err != nil {
		return err
	}
	return nil
}

// ValidateMCPServers validates that all MCP servers have required fields.
//
// Expected:
//   - servers is a slice of MCPServerConfig.
//
// Returns:
//   - An error if any server is missing Name or Command, nil otherwise.
//
// Side effects:
//   - None.
func ValidateMCPServers(servers []MCPServerConfig) error {
	for i, server := range servers {
		if server.Name == "" {
			return fmt.Errorf("MCP server at index %d: missing required field 'name'", i)
		}
		if server.Command == "" {
			return fmt.Errorf("MCP server at index %d: missing required field 'command'", i)
		}
	}
	return nil
}

// applyDefaults populates missing configuration fields with sensible defaults.
//
// Expected:
//   - cfg is a non-nil AppConfig pointer.
//
// Side effects:
//   - Modifies cfg in place, filling empty fields with default values from DefaultConfig.
func applyDefaults(cfg *AppConfig) {
	defaults := DefaultConfig()

	if cfg.Providers.Default == "" {
		cfg.Providers.Default = defaults.Providers.Default
	}
	applyProviderDefaults(&cfg.Providers.Ollama, defaults.Providers.Ollama)
	applyProviderDefaults(&cfg.Providers.OpenAI, defaults.Providers.OpenAI)
	applyProviderDefaults(&cfg.Providers.Anthropic, defaults.Providers.Anthropic)

	if cfg.AgentDir == "" {
		cfg.AgentDir = defaults.AgentDir
	}
	if cfg.SkillDir == "" {
		cfg.SkillDir = defaults.SkillDir
	}
	if cfg.DataDir == "" {
		cfg.DataDir = defaults.DataDir
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = defaults.LogLevel
	}
	if cfg.DefaultAgent == "" {
		cfg.DefaultAgent = defaults.DefaultAgent
	}
	cfg.CategoryRouting = mergeCategoryRouting(defaults.CategoryRouting, cfg.CategoryRouting)
	if cfg.Plugins.Dir == "" {
		cfg.Plugins.Dir = filepath.Join(homeDir(), ".config", "flowstate", "plugins")
	}
	if cfg.Plugins.Timeout == 0 {
		cfg.Plugins.Timeout = 5
	}

	if len(cfg.Plugins.Failover.Tiers) == 0 {
		cfg.Plugins.Failover.Tiers = defaults.Plugins.Failover.Tiers
	}

	if len(cfg.ToolCapableModels) == 0 {
		cfg.ToolCapableModels = defaults.ToolCapableModels
	}
	if len(cfg.ToolIncapableModels) == 0 {
		cfg.ToolIncapableModels = defaults.ToolIncapableModels
	}

	if !cfg.Harness.Enabled {
		cfg.Harness.Enabled = true
	}
	if cfg.Harness.MaxRetries == 0 {
		cfg.Harness.MaxRetries = defaults.Harness.MaxRetries
	}

	for i := range cfg.MCPServers {
		if !cfg.MCPServers[i].Enabled {
			cfg.MCPServers[i].Enabled = true
		}
	}

	applyCompressionDefaults(&cfg.Compression, defaults.Compression)
}

// applyCompressionDefaults fills empty numeric and path fields of the
// CompressionConfig from defaults, leaving any explicitly configured
// value untouched. Enabled flags are never overridden — an explicit
// false in YAML is preserved because all defaults are false too.
//
// Expected:
//   - cfg is a non-nil CompressionConfig pointer.
//   - defaults carries the values returned by DefaultCompressionConfig.
//
// Side effects:
//   - Modifies cfg in place.
func applyCompressionDefaults(cfg *contextpkg.CompressionConfig, defaults contextpkg.CompressionConfig) {
	if cfg.MicroCompaction.HotTailSize == 0 {
		cfg.MicroCompaction.HotTailSize = defaults.MicroCompaction.HotTailSize
	}
	if cfg.MicroCompaction.TokenThreshold == 0 {
		cfg.MicroCompaction.TokenThreshold = defaults.MicroCompaction.TokenThreshold
	}
	if cfg.MicroCompaction.StorageDir == "" {
		cfg.MicroCompaction.StorageDir = defaults.MicroCompaction.StorageDir
	}
	if cfg.MicroCompaction.PlaceholderTokens == 0 {
		cfg.MicroCompaction.PlaceholderTokens = defaults.MicroCompaction.PlaceholderTokens
	}
	if cfg.MicroCompaction.IdleTTL == 0 {
		cfg.MicroCompaction.IdleTTL = defaults.MicroCompaction.IdleTTL
	}
	if cfg.AutoCompaction.Threshold == 0 {
		cfg.AutoCompaction.Threshold = defaults.AutoCompaction.Threshold
	}
	if cfg.SessionMemory.StorageDir == "" {
		cfg.SessionMemory.StorageDir = defaults.SessionMemory.StorageDir
	}
	if cfg.SessionMemory.WaitTimeout == 0 {
		cfg.SessionMemory.WaitTimeout = defaults.SessionMemory.WaitTimeout
	}
}

// mergeCategoryRouting applies user overrides on top of the default routing map.
//
// Expected:
//   - defaults contains the base category routing configuration.
//   - overrides contains user-specified replacements.
//
// Returns:
//   - A merged map with overrides applied over defaults.
//
// Side effects:
//   - None.
func mergeCategoryRouting(defaults, overrides map[string]engine.CategoryConfig) map[string]engine.CategoryConfig {
	merged := make(map[string]engine.CategoryConfig, len(defaults))
	for key, value := range defaults {
		merged[key] = value
	}
	for key, value := range overrides {
		merged[key] = value
	}
	return merged
}

// applyProviderDefaults populates missing provider configuration fields with defaults.
//
// Expected:
//   - cfg is a non-nil ProviderConfig pointer.
//   - defaults is a ProviderConfig with fallback values.
//
// Side effects:
//   - Modifies cfg in place, filling empty Host, APIKey, and Model fields from defaults.
func applyProviderDefaults(cfg *ProviderConfig, defaults ProviderConfig) {
	if cfg.Host == "" {
		cfg.Host = defaults.Host
	}
	if cfg.APIKey == "" {
		cfg.APIKey = defaults.APIKey
	}
	if cfg.Model == "" {
		cfg.Model = defaults.Model
	}
	if cfg.OAuth.ClientID == "" {
		cfg.OAuth.ClientID = defaults.OAuth.ClientID
	}
	if cfg.OAuth.Scopes == "" {
		cfg.OAuth.Scopes = defaults.OAuth.Scopes
	}
}

// expandTilde expands a leading ~ or ~/ in a path to the user's home directory.
//
// Expected:
//   - path is a filesystem path that may begin with ~ or ~/.
//
// Returns:
//   - The expanded path, or the original path when no tilde prefix is present.
//
// Side effects:
//   - None.
func expandTilde(path string) string {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return home
	}
	if len(path) > 2 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

// expandPaths expands tildes in all relevant AppConfig path fields.
//
// Expected:
//   - cfg is a non-nil AppConfig pointer.
//
// Side effects:
//   - Modifies cfg in place.
func expandPaths(cfg *AppConfig) {
	cfg.AgentDir = expandTilde(cfg.AgentDir)
	cfg.SkillDir = expandTilde(cfg.SkillDir)
	cfg.DataDir = expandTilde(cfg.DataDir)
	cfg.PlanLocation = expandTilde(cfg.PlanLocation)
	cfg.Plugins.Dir = expandTilde(cfg.Plugins.Dir)
	for i, dir := range cfg.AgentDirs {
		cfg.AgentDirs[i] = expandTilde(dir)
	}
	cfg.Compression.MicroCompaction.StorageDir = expandTilde(cfg.Compression.MicroCompaction.StorageDir)
	cfg.Compression.SessionMemory.StorageDir = expandTilde(cfg.Compression.SessionMemory.StorageDir)
}
