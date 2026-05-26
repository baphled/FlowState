package toolset

import (
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/learning"
	recall "github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/bash"
	toolmemory "github.com/baphled/flowstate/internal/tool/memory"
	"github.com/baphled/flowstate/internal/tool/pathguard"
	"github.com/baphled/flowstate/internal/tool/plan"
	"github.com/baphled/flowstate/internal/tool/read"
	toolrecall "github.com/baphled/flowstate/internal/tool/recall"
	skilltool "github.com/baphled/flowstate/internal/tool/skill"
	toolswarm "github.com/baphled/flowstate/internal/tool/swarm"
	todotool "github.com/baphled/flowstate/internal/tool/todo"
	toolsvault "github.com/baphled/flowstate/internal/tool/vault"
	"github.com/baphled/flowstate/internal/tool/web"
	"github.com/baphled/flowstate/internal/tool/write"
)

// DefaultVaultCollection is the canonical Qdrant collection name for
// vault-rag indices. It is consumed by buildVaultQueryHandler (the
// in-process mcp_vault-rag_query_vault read tool) and by the
// vault_index / vault_sync admin tools registered through
// AppendVaultIndexTools. Exposed as a package-level constant so every
// site that names a collection resolves the same fallback when the
// operator has not overridden cfg.VaultCollection.
const DefaultVaultCollection = "flowstate-vault"

// BuildAppTools returns the base tool slice the FlowState engine starts
// with: bash, read, write, web, the skill loader, the todowrite + todo_update
// pair, and the plan_list/plan_read read-only plan tools bound to plansDir.
// The slice is the canonical seed for an engine's tool registry; callers
// compose conditional tools on top of it via the Append* helpers below.
//
// Both todo tools share a single todoStore so todo_update patches the same
// per-session list that todowrite creates.
//
// When guard is non-nil, the three mutating-filesystem tools in this
// base slice (bash, read, write) are constructed via NewWithGuard so
// the main engine's per-call dispatch routes through
// pathguard.Guard.CheckForTool. Without this wiring the main engine
// dispatched guard-less tool instances and silently bypassed the
// permissions.yaml overlay (Slices A+B+C) AND the Plan-mode
// output-dir overlay (Slice 1, cbe4464e) — the engine schema filter
// closed the LLM-visible surface for Plan mode, but a permissive
// provider that emitted an out-of-schema tool call would land on
// guard-less Execute and the file write would succeed. Mirrors the
// per-manifest tool factory at app.buildToolsForManifestWithStore so
// both engines (main + delegate) honour the same pathguard
// decisions. A nil guard preserves the legacy fully-permissive
// behaviour for tests and other call sites that explicitly do not
// want pathguard enforcement.
//
// Expected:
//   - skillLoader is the FileSkillLoader used by the skill_load tool.
//   - todoStore backs both the todowrite and todo_update tools.
//   - plansDir is the resolved plan directory; an empty string is
//     permitted for tests that do not exercise the plan tools.
//   - guard, when non-nil, is wired into bash/read/write so
//     CheckForTool fires on every Execute. May be nil.
//
// Returns:
//   - The base tool slice; the caller appends domain tools and registers
//     the result.
//
// Side effects:
//   - None.
func BuildAppTools(skillLoader *skill.FileSkillLoader, todoStore todotool.Store, plansDir string, guard *pathguard.Guard) []tool.Tool {
	var (
		bashTool  tool.Tool
		readTool  tool.Tool
		writeTool tool.Tool
	)
	if guard != nil {
		bashTool = bash.NewWithGuard(guard)
		readTool = read.NewWithGuard(guard)
		writeTool = write.NewWithGuard(guard)
	} else {
		bashTool = bash.New()
		readTool = read.New()
		writeTool = write.New()
	}
	return []tool.Tool{
		bashTool,
		readTool,
		writeTool,
		web.New(),
		skilltool.New(skillLoader),
		todotool.New(todoStore),
		todotool.NewUpdate(todoStore),
		plan.NewList(plansDir),
		plan.NewRead(plansDir),
	}
}

// AppendSwarmTools appends swarm_list, swarm_info, and swarm_validate when
// a swarm registry is available. Returns base unchanged when reg is nil.
func AppendSwarmTools(base []tool.Tool, reg *swarm.Registry) []tool.Tool {
	if reg == nil {
		return base
	}
	return append(base,
		toolswarm.NewSwarmListTool(reg),
		toolswarm.NewSwarmInfoTool(reg),
		toolswarm.NewSwarmValidateTool(reg),
	)
}

// AppendMemoryTools appends the native mcp_memory_search_nodes and
// mcp_memory_open_nodes tools when a MemoryClient is available. Returns
// base unchanged when client is nil (Qdrant not configured).
func AppendMemoryTools(base []tool.Tool, client learning.MemoryClient) []tool.Tool {
	if client == nil {
		return base
	}
	return append(base,
		toolmemory.NewSearchNodesTool(client),
		toolmemory.NewOpenNodesTool(client),
	)
}

// AppendVaultTools appends the native mcp_vault-rag_query_vault tool when
// a vault Handler is available. Returns base unchanged when handler is nil
// (Qdrant not configured or vault collection unavailable).
func AppendVaultTools(base []tool.Tool, handler toolsvault.Handler) []tool.Tool {
	if handler == nil {
		return base
	}
	return append(base, toolsvault.NewQueryVaultTool(handler))
}

// AppendVaultIndexTools appends the vault_index and vault_sync tools when
// the app config has both a vault path and a Qdrant URL configured.
// Returns base unchanged when either is absent.
func AppendVaultIndexTools(base []tool.Tool, cfg *config.AppConfig) []tool.Tool {
	if cfg == nil || cfg.VaultPath == "" || cfg.Qdrant.URL == "" {
		return base
	}
	collection := cfg.VaultCollection
	if collection == "" {
		collection = DefaultVaultCollection
	}
	ollamaHost := cfg.Providers.Ollama.Host
	if ollamaHost == "" {
		ollamaHost = "http://localhost:11434"
	}
	idxCfg := toolsvault.IndexerConfig{
		VaultRoot:      cfg.VaultPath,
		Collection:     collection,
		QdrantURL:      cfg.Qdrant.URL,
		OllamaHost:     ollamaHost,
		EmbeddingModel: cfg.ResolvedEmbeddingModel(),
	}
	return append(base,
		toolsvault.NewIndexVaultTool(idxCfg),
		toolsvault.NewSyncVaultTool(idxCfg),
	)
}

// AppendChainTools appends the chain_search and chain_get_messages tools
// when a chain context store is available. Returns base unchanged when cs
// is nil (recall pipeline disabled).
func AppendChainTools(base []tool.Tool, cs recall.ChainContextStore) []tool.Tool {
	if cs == nil {
		return base
	}
	return append(base,
		toolrecall.NewChainSearchTool(cs),
		toolrecall.NewChainGetMessagesTool(cs),
	)
}
