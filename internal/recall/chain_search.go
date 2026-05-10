package recall

import (
	"context"
	"errors"
	"time"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

// ChainSearchTool provides search functionality across message chains.
type ChainSearchTool struct {
	chainStore ChainContextStore
	embedder   provider.Provider
	store      *FileContextStore
	topK       int
	bus        *eventbus.EventBus
}

// NewChainSearchTool creates a new ChainSearchTool.
//
// Expected:
//   - chainStore provides chain access.
//   - embedder provides embedding support.
//   - store provides context access.
//
// Returns:
//   - A chain search tool.
//
// Side effects:
//   - None.
func NewChainSearchTool(
	chainStore ChainContextStore, embedder provider.Provider,
	store *FileContextStore, bus *eventbus.EventBus,
) *ChainSearchTool {
	return &ChainSearchTool{
		chainStore: chainStore,
		embedder:   embedder,
		store:      store,
		topK:       5,
		bus:        bus,
	}
}

// Name returns the name of the tool.
//
// Expected:
//   - The receiver is a valid ChainSearchTool.
//
// Returns:
//   - The tool name.
//
// Side effects:
//   - None.
func (t *ChainSearchTool) Name() string {
	return "chain_search"
}

// Description returns a description of the tool.
//
// Expected:
//   - The receiver is a valid ChainSearchTool.
//
// Returns:
//   - A short tool description.
//
// Side effects:
//   - None.
func (t *ChainSearchTool) Description() string {
	return "Search cross-agent context from the delegation chain"
}

// Schema returns the JSON schema for the tool parameters.
//
// Expected:
//   - The receiver is a valid ChainSearchTool.
//
// Returns:
//   - The tool parameter schema.
//
// Side effects:
//   - None.
func (t *ChainSearchTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"query":    {Type: "string", Description: "Search query"},
			"agent_id": {Type: "string", Description: "Filter by agent ID (optional)"},
		},
		Required: []string{"query"},
	}
}

// Execute performs the chain search operation.
//
// Expected:
//   - ctx is valid for chain lookups.
//   - input contains a query string.
//
// Returns:
//   - A tool result containing formatted chain messages. On a genuine
//     Search failure the Output still carries the recency-fallback
//     text so the engine's tool loop continues to have something to
//     hand the model, but the second return is non-nil so the engine
//     publishes tool.execute.error and consumers can surface the
//     real failure on the wire.
//   - An error when the underlying Search call fails. Zero-result
//     queries on a healthy Search path are NOT errors — only genuine
//     failures (Qdrant unavailable, embedding-model dimension
//     mismatch, network timeout, broker fan-out exhaustion) are
//     surfaced this way. See M9 in Bug Hunt Findings (May 2026).
//
// Side effects:
//   - Reads from the chain store.
//   - Publishes a recall.chain.searched event for every call (success
//     or zero-result) and additionally publishes
//     recall.chain.search.failed when the Search call errors. The two
//     topics are independently subscribable so existing observability
//     keeps working and new subscribers can react to genuine failure.
func (t *ChainSearchTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	query, ok := input.Arguments["query"].(string)
	if !ok || query == "" {
		return t.fallbackToRecent()
	}

	start := time.Now()
	agentID, ok := input.Arguments["agent_id"].(string)
	if !ok {
		agentID = ""
	}

	results, err := t.chainStore.Search(ctx, query, t.topK)
	latencyMS := time.Since(start).Milliseconds()

	sessionID := ""
	if sid, ok := ctx.Value(session.IDKey{}).(string); ok {
		sessionID = sid
	}

	if t.bus != nil {
		t.bus.Publish(events.EventRecallChainSearched, events.NewRecallChainSearchEvent(events.RecallChainSearchEventData{
			SessionID: sessionID,
			AgentID:   agentID,
			Query:     query,
			Results:   len(results),
			LatencyMS: latencyMS,
		}))
	}

	// M9: genuine Search failure must be observable separately from
	// "zero results". The pre-fix code branched on
	// `err != nil || len(results) == 0` into the same silent
	// recency fallback, masking Qdrant outages and dimension
	// mismatches as benign empty queries.
	if err != nil {
		if t.bus != nil {
			t.bus.Publish(events.EventRecallChainSearchFailed, events.NewRecallChainSearchFailedEvent(events.RecallChainSearchFailedEventData{
				SessionID: sessionID,
				AgentID:   agentID,
				Query:     query,
				Reason:    err.Error(),
				ErrType:   classifyChainSearchErr(err),
				LatencyMS: latencyMS,
			}))
		}
		// Still hand the model recency-fallback output so the
		// historical UX (no hard runtime error reaching the model)
		// is preserved; the engine treats the non-nil err as a
		// soft-fail at engine.go:3825 (`result.Error = err`) and
		// surfaces it via tool.execute.error.
		fallback, _ := t.fallbackToRecent()
		fallback.Error = err
		return fallback, err
	}

	if len(results) == 0 {
		return t.fallbackToRecent()
	}

	return tool.Result{Output: formatMessages(extractMessages(results))}, nil
}

// classifyChainSearchErr maps an underlying Search error to a coarse
// classifier string for dashboards and structured logging. The
// classifier vocabulary is open (subscribers must tolerate unknown
// values) but stable for well-known categories so a Grafana panel can
// group by ErrType without parsing message text. Currently best-effort:
// we surface ErrAllSourcesFailed when the broker is the underlying
// source, and "store.search" otherwise. Future contributors should
// extend this as new well-known failure modes emerge (e.g.
// "embedder.dim_mismatch" once M10 lands).
func classifyChainSearchErr(err error) string {
	if errors.Is(err, ErrAllSourcesFailed) {
		return "broker.all_sources_failed"
	}
	return "store.search"
}

// fallbackToRecent returns recent chain messages when a query cannot be used.
//
// Expected:
//   - The receiver is a valid ChainSearchTool.
//
// Returns:
//   - A tool result containing recent messages when available.
//   - An error when retrieving recent messages fails.
//
// Side effects:
//   - Reads from the chain store.
func (t *ChainSearchTool) fallbackToRecent() (tool.Result, error) {
	messages, err := t.chainStore.GetByAgent("", t.topK)
	if err != nil || len(messages) == 0 {
		return tool.Result{Output: ""}, err
	}
	return tool.Result{Output: formatMessages(messages)}, nil
}
