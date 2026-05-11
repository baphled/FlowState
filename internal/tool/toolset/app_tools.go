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

// DefaultVaultCollection is the Qdrant collection used by
// flowstate-vault-server. Exposed as a constant so callers that build
// vault-index tooling can resolve the same fallback the composition root
// historically used.
const DefaultVaultCollection = "flowstate-vault"

// BuildAppTools returns the base tool slice the FlowState engine starts
// with: bash, read, write, web, the skill loader, the todo tool, and the
// plan_list/plan_read read-only plan tools bound to plansDir. The slice is
// the canonical seed for an engine's tool registry; callers compose
// conditional tools on top of it via the Append* helpers below.
//
// Expected:
//   - skillLoader is the FileSkillLoader used by the skill_load tool.
//   - todoStore backs the todowrite tool.
//   - plansDir is the resolved plan directory; an empty string is
//     permitted for tests that do not exercise the plan tools.
//
// Returns:
//   - The base tool slice; the caller appends domain tools and registers
//     the result.
//
// Side effects:
//   - None.
func BuildAppTools(skillLoader *skill.FileSkillLoader, todoStore todotool.Store, plansDir string) []tool.Tool {
	return []tool.Tool{
		bash.New(),
		read.New(),
		write.New(),
		web.New(),
		skilltool.New(skillLoader),
		todotool.New(todoStore),
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
