package lifecycle

import (
	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
)

// ContextAssemblyCtx carries the assembled context data for the
// ContextAssembly stage. Hooks in this stage can enrich, compress, recall,
// or rehydrate the message window before streaming.
type ContextAssemblyCtx struct {
	Messages    []provider.Message
	TokenBudget int
	Manifest    *agent.Manifest
}
