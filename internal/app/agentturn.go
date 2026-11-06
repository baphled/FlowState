package app

// agentturn.go implements the M2 agent turn loop: a bounded
// multi-round provider turn in which the model drives allowlisted
// session tools (fs.read/fs.write/fs.edit/exec.run) to complete an
// SDLC micro-packet. It composes the existing ports — provider
// Streamer, tool Registry/Loop, store single writer — and owns no
// new lifecycle responsibility (principal review 2026-11-06:
// placement in internal/app, reuse tool.Loop.Invoke on the turn's
// own event writer, hand-written schemas with drift-guard tests).

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// Agent loop sizing (principal review: constants, not literals).
const (
	// DefaultAgentMaxRounds caps tool rounds per agent turn.
	DefaultAgentMaxRounds = 12
	// AgentToolResultBudget caps a tool result rendered into model
	// context; the full result stays in the persisted tool.result.
	AgentToolResultBudget = 4096
)

// AgentTurnOptions configures one agent turn.
type AgentTurnOptions struct {
	// Allow is the deny-by-default tool allowlist.
	Allow []string
	// Workdir confines fs/exec tools; empty defaults beside the db.
	Workdir string
	// DBPath locates the session workdir root when Workdir is empty.
	DBPath string
	// MaxRounds caps tool rounds; <=0 uses DefaultAgentMaxRounds.
	MaxRounds int
	// Budget caps content+tool-result bytes; <=0 means unlimited.
	Budget int64
	// MaxTokens caps each provider response; <=0 provider default.
	MaxTokens int64
	// ModelID overrides the session model when non-empty.
	ModelID string
	// Tools injects extra tools alongside the built-ins (tests).
	Tools []ToolAdapter
}

// AgentTurnResult reports one agent turn.
type AgentTurnResult struct {
	SessionID      string
	FinalContent   string
	Rounds         int
	ToolCalls      int
	BudgetExceeded bool
	Consumed       int64
}

// ToolAdapter injects one extra tool alongside the built-ins.
type ToolAdapter struct {
	Name string
	Tool tool.Tool
}

// agentSchemas hand-writes one JSON-Schema (object) per built-in
// tool; drift is guarded by TestAgentSchemaConformance.
func agentSchemas() map[string]string {
	return map[string]string{
		"echo":     `{"type":"object"}`,
		"fs.read":  `{"type":"object","required":["path"],"properties":{"path":{"type":"string","description":"Path relative to the session workdir"}}}`,
		"fs.write": `{"type":"object","required":["path","content"],"properties":{"path":{"type":"string"},"content":{"type":"string"}}}`,
		"fs.edit":  `{"type":"object","required":["path","old_text","new_text"],"properties":{"path":{"type":"string"},"old_text":{"type":"string"},"new_text":{"type":"string"},"all":{"type":"boolean"}}}`,
		"exec.run": `{"type":"object","required":["cmd"],"properties":{"cmd":{"type":"string","enum":["git","gh","go","make"],"description":"Allow-listed binary"},"args":{"type":"array","items":{"type":"string"},"maxItems":32}}}`,
	}
}

func agentDescriptions() map[string]string {
	return map[string]string{
		"echo":     "Return the arguments back (wiring probe).",
		"fs.read":  "Read a text file inside the session workdir.",
		"fs.write": "Write a text file inside the session workdir.",
		"fs.edit":  "Replace old_text with new_text in a file inside the session workdir.",
		"exec.run": "Run an allow-listed binary (git/gh/go/make) with approved subcommands in the session workdir.",
	}
}

// buildAgentRegistry assembles the session tool registry; exec.run
// registers only behind its double env gate (same as toolrun.go).
func buildAgentRegistry(workdir string, extra []ToolAdapter) (*tool.Registry, error) {
	registry := tool.NewRegistry()
	builtins := []tool.Tool{tool.NewEcho(), tool.NewFSRead(workdir), tool.NewFSWrite(workdir), tool.NewFSEdit(workdir)}
	if tool.ExecEnabled() {
		builtins = append(builtins, tool.NewExecRun(workdir))
	}
	for _, t := range builtins {
		if err := registry.Register(t); err != nil {
			return nil, err
		}
	}
	for _, a := range extra {
		if err := registry.Register(a.Tool); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// advertise renders the allowlisted tools as provider definitions.
func advertise(registry *tool.Registry, allow []string) ([]provider.ToolDefinition, error) {
	schemas, descriptions := agentSchemas(), agentDescriptions()
	out := make([]provider.ToolDefinition, 0, len(allow))
	for _, name := range allow {
		if _, err := registry.Get(name); err != nil {
			return nil, fmt.Errorf("agent: allowlisted tool %q is not registered: %w", name, err)
		}
		def := provider.ToolDefinition{Name: name, Parameters: json.RawMessage(schemas[name])}
		if d, ok := descriptions[name]; ok {
			def.Description = d
		} else if def.Parameters == nil {
			def.Parameters = json.RawMessage(`{"type":"object"}`)
		}
		out = append(out, def)
	}
	return out, nil
}

// AgentTurn runs one agent turn with default options.
func AgentTurn(ctx context.Context, st *store.Store, sess *store.Session, p provider.Streamer) (*AgentTurnResult, error) {
	return AgentTurnWithOptions(ctx, st, sess, p, AgentTurnOptions{})
}
