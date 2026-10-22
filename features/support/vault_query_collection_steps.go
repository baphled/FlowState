//go:build e2e

package support

import (
	"context"
	"errors"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/recall/qdrant"
	"github.com/baphled/flowstate/internal/vaultindex"
)

var (
	errNoVaultQueryHandler           = errors.New("no vault query handler has been constructed in this scenario")
	errSearchedWrongCollectionFormat = "the search targeted collection %q, expected %q"
	errVaultQueryFailedWrapFormat    = "the vault query failed: %w"
	errSearcherNotExercisedFormat    = "the recording searcher recorded %d searches, expected 1"
)

// vaultQueryCollectionState holds scenario-scoped state for the
// per-vault query collection resolution scenarios.
type vaultQueryCollectionState struct {
	embedder *fixedVectorEmbedder
	searcher *recordingVaultSearcher
	handler  *vaultindex.QueryHandler
	queryErr error
}

// fixedVectorEmbedder is a vaultindex.Embedder that returns a fixed
// vector for every call so scenarios need no live embedding provider.
type fixedVectorEmbedder struct {
	vector []float64
}

// Embed returns the canned vector, ignoring the request text.
func (e *fixedVectorEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	return e.vector, nil
}

// recordingVaultSearcher is a vaultindex.Searcher that records every
// Search call's collection name and returns a single canned hit so
// scenarios need no live Qdrant.
type recordingVaultSearcher struct {
	collections []string
}

// Search records the requested collection and returns one scored point.
func (s *recordingVaultSearcher) Search(_ context.Context, collection string, _ []float64, _ int) ([]qdrant.ScoredPoint, error) {
	s.collections = append(s.collections, collection)
	return []qdrant.ScoredPoint{{
		ID: "point-1",
		Payload: map[string]any{
			"content":     "canned vault hit",
			"source_file": "notes/a.md",
			"chunk_index": 0,
		},
	}}, nil
}

// RegisterVaultQueryCollectionSteps registers the per-vault query
// collection resolution steps with the Godog scenario context.
//
// Expected:
//   - ctx is a valid Godog ScenarioContext.
//
// Side effects:
//   - Registers all vault query collection scenario steps with the
//     provided context.
func RegisterVaultQueryCollectionSteps(ctx *godog.ScenarioContext) {
	state := &vaultQueryCollectionState{}

	ctx.Step(`^a vault query handler with default collection "([^"]*)"$`, state.aVaultQueryHandlerWithDefaultCollection)
	ctx.Step(`^an agent queries the vault without a vault argument$`, state.anAgentQueriesTheVaultWithoutAVaultArgument)
	ctx.Step(`^an agent queries the vault with vault argument "([^"]*)"$`, state.anAgentQueriesTheVaultWithVaultArgument)
	ctx.Step(`^the search should target collection "([^"]*)"$`, state.theSearchShouldTargetCollection)

	ctx.After(state.clearScenarioState)
}

// aVaultQueryHandlerWithDefaultCollection constructs a fresh query
// handler wired to a fixed embedder and a recording searcher, resetting
// all scenario state so every scenario starts clean.
//
// Expected:
//   - collection is the default Qdrant collection the handler falls
//     back to when no vault scope resolves.
//
// Returns:
//   - Nil; handler construction cannot fail.
//
// Side effects:
//   - Replaces the scenario's embedder, searcher, handler, and recorded
//     query error.
func (s *vaultQueryCollectionState) aVaultQueryHandlerWithDefaultCollection(collection string) error {
	s.embedder = &fixedVectorEmbedder{vector: []float64{1, 2, 3}}
	s.searcher = &recordingVaultSearcher{}
	s.handler = vaultindex.NewQueryHandler(s.embedder, s.searcher, collection)
	s.queryErr = nil
	return nil
}

// anAgentQueriesTheVaultWithoutAVaultArgument runs a query through the
// handler with no vault scope.
//
// Returns:
//   - An error when no handler has been constructed.
//
// Side effects:
//   - Records the query error, if any, on the scenario state.
func (s *vaultQueryCollectionState) anAgentQueriesTheVaultWithoutAVaultArgument() error {
	return s.runVaultQuery("")
}

// anAgentQueriesTheVaultWithVaultArgument runs a query through the
// handler with the given vault argument.
//
// Expected:
//   - vault is the raw vault argument from the tool call; any casing
//     or spacing is accepted.
//
// Returns:
//   - An error when no handler has been constructed.
//
// Side effects:
//   - Records the query error, if any, on the scenario state.
func (s *vaultQueryCollectionState) anAgentQueriesTheVaultWithVaultArgument(vault string) error {
	return s.runVaultQuery(vault)
}

// theSearchShouldTargetCollection asserts the query drove the searcher
// exactly once against the expected collection.
//
// Expected:
//   - collection is the Qdrant collection the search must have hit.
//
// Returns:
//   - An error when the query failed, the searcher was not exercised
//     exactly once, or the recorded collection differs.
//
// Side effects:
//   - None.
func (s *vaultQueryCollectionState) theSearchShouldTargetCollection(collection string) error {
	if s.queryErr != nil {
		return fmt.Errorf(errVaultQueryFailedWrapFormat, s.queryErr)
	}
	if len(s.searcher.collections) != 1 {
		return fmt.Errorf(errSearcherNotExercisedFormat, len(s.searcher.collections))
	}
	if s.searcher.collections[0] != collection {
		return fmt.Errorf(errSearchedWrongCollectionFormat, s.searcher.collections[0], collection)
	}
	return nil
}

// runVaultQuery drives the scenario's handler with a non-empty
// question and the given vault scope, storing any failure for the
// assertion steps.
//
// Expected:
//   - vault is the raw vault argument; empty means unscoped.
//
// Returns:
//   - An error when no handler has been constructed.
//
// Side effects:
//   - Exercises the handler's embedder and searcher.
func (s *vaultQueryCollectionState) runVaultQuery(vault string) error {
	if s.handler == nil {
		return errNoVaultQueryHandler
	}
	_, s.queryErr = s.handler.Handle(context.Background(), vaultindex.QueryArgs{
		Question: "what do my vault notes say?",
		Vault:    vault,
	})
	return nil
}

// clearScenarioState is the Godog After hook dropping the scenario's
// handler wiring so nothing survives across scenarios.
//
// Expected:
//   - ctx is the scenario context.
//   - scenario is the finished scenario.
//
// Returns:
//   - The context, unchanged.
//   - Nil error.
//
// Side effects:
//   - Clears the embedder, searcher, handler, and query error.
func (s *vaultQueryCollectionState) clearScenarioState(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
	s.embedder = nil
	s.searcher = nil
	s.handler = nil
	s.queryErr = nil
	return ctx, nil
}
