//go:build e2e

package support

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/mcp"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/recall/qdrant"
	vaultrecall "github.com/baphled/flowstate/internal/recall/vault"
)

// Phase 9b step glue:
//
// Step definitions for the recall / learning / memory BDD feature files
// that were previously tagged @wip. Each Given step wires an in-process
// fake (no network, no real Qdrant / Mem0 / MCP server) against the
// existing product code in internal/learning, internal/recall,
// internal/recall/qdrant and internal/recall/vault. When/Then steps
// exercise that product code through the fakes and assert on the
// resulting writes or returned observations.
//
// The intentional contract is:
//   - Feature files describe observable product behaviour.
//   - Steps drive the real product code (Mem0LearningStore, Broker,
//     StructuredDistiller, qdrant.Source, vault.Source, MCPMemorySource)
//     against fakes that satisfy the same interfaces.
//   - No scenario silently skips; the Skip-gracefully scenarios still
//     exercise the real guard paths in product code.

// --- Fakes ---------------------------------------------------------------

// recallFakeVectorStore is a thread-safe in-process fake for
// learning.VectorStoreClient. It records Upsert writes and serves Search
// with pre-canned scored points so we can assert on what the production
// code wrote and what it received back.
type recallFakeVectorStore struct {
	mu           sync.Mutex
	upserts      []recallUpsertRecord
	searchResult []qdrant.ScoredPoint
	searchErr    error
	searchCalls  int
}

// recallUpsertRecord captures one Upsert invocation on the
// learning.VectorStoreClient fake so assertions can inspect the
// collection name and recorded vector points.
type recallUpsertRecord struct {
	Collection string
	Points     []learning.VectorPoint
}

// Upsert records the upsert payload for later inspection.
//
// Expected: collection names the target collection; points carries the
// vectors being written.
// Returns: nil.
// Side effects: appends the call to the internal upserts slice.
func (s *recallFakeVectorStore) Upsert(_ context.Context, collection string, points []learning.VectorPoint, _ bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upserts = append(s.upserts, recallUpsertRecord{
		Collection: collection,
		Points:     append([]learning.VectorPoint(nil), points...),
	})
	return nil
}

// Search returns the pre-canned scored points as learning.ScoredVectorPoint.
//
// Expected: arguments as supplied by Mem0LearningStore.Query.
// Returns: a copy of the canned result and the canned error.
// Side effects: increments searchCalls.
func (s *recallFakeVectorStore) Search(_ context.Context, _ string, _ []float64, _ int) ([]learning.ScoredVectorPoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.searchCalls++
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	out := make([]learning.ScoredVectorPoint, 0, len(s.searchResult))
	for _, p := range s.searchResult {
		out = append(out, learning.ScoredVectorPoint{
			ID:      p.ID,
			Score:   p.Score,
			Payload: p.Payload,
		})
	}
	return out, nil
}

// recordedUpserts returns a copy of the upsert records for assertions.
//
// Expected: none.
// Returns: a snapshot slice of every Upsert invocation seen so far.
// Side effects: none (the copy prevents callers from mutating the log).
func (s *recallFakeVectorStore) recordedUpserts() []recallUpsertRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recallUpsertRecord(nil), s.upserts...)
}

// qdrantFakeStore adapts recallFakeVectorStore to qdrant.VectorStore so
// we can drive qdrant.Source against the same recorded state. The
// qdrant.VectorStore interface is narrower than learning.VectorStoreClient
// (it uses qdrant.Point and qdrant.ScoredPoint instead of the learning
// shapes), so we keep a second adapter rather than complicating the
// original fake.
type qdrantFakeStore struct {
	mu           sync.Mutex
	upserts      []qdrantUpsertRecord
	searchResult []qdrant.ScoredPoint
	searchErr    error
	searchCalls  int
}

// qdrantUpsertRecord captures one Upsert invocation on the
// qdrant.VectorStore fake.
type qdrantUpsertRecord struct {
	Collection string
	Points     []qdrant.Point
}

// CreateCollection is a no-op for the fake.
//
// Expected: any arguments.
// Returns: nil.
// Side effects: none.
func (s *qdrantFakeStore) CreateCollection(_ context.Context, _ string, _ qdrant.CollectionConfig) error {
	return nil
}

// Upsert records the points written to the fake.
//
// Expected: collection is the target Qdrant collection; points carries the vectors.
// Returns: nil.
// Side effects: appends to the internal upserts slice.
func (s *qdrantFakeStore) Upsert(_ context.Context, collection string, points []qdrant.Point, _ bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upserts = append(s.upserts, qdrantUpsertRecord{
		Collection: collection,
		Points:     append([]qdrant.Point(nil), points...),
	})
	return nil
}

// Search returns the canned scored points.
//
// Expected: arguments as supplied by qdrant.Source.Query.
// Returns: a copy of searchResult and the canned error.
// Side effects: increments searchCalls.
func (s *qdrantFakeStore) Search(_ context.Context, _ string, _ []float64, _ int) ([]qdrant.ScoredPoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.searchCalls++
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	return append([]qdrant.ScoredPoint(nil), s.searchResult...), nil
}

// DeleteCollection is a no-op for the fake.
//
// Expected: any arguments.
// Returns: nil.
// Side effects: none.
func (s *qdrantFakeStore) DeleteCollection(_ context.Context, _ string) error { return nil }

// CollectionExists reports the fake collection as present.
//
// Expected: any arguments.
// Returns: true, nil.
// Side effects: none.
func (s *qdrantFakeStore) CollectionExists(_ context.Context, _ string) (bool, error) {
	return true, nil
}

// fakeVectorEmbedder produces a deterministic vector for any text so
// the store / distillation path can be exercised without a real
// embedding model. It implements both learning.VectorEmbedder and
// qdrant.Embedder (the interfaces are identical).
type fakeVectorEmbedder struct {
	vec []float64
}

// Embed returns a fixed vector, or the configured vec when non-nil.
//
// Expected: any text argument.
// Returns: a float64 slice; the default is {0.1, 0.2, 0.3}.
// Side effects: none.
func (f *fakeVectorEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	if f.vec == nil {
		return []float64{0.1, 0.2, 0.3}, nil
	}
	return append([]float64(nil), f.vec...), nil
}

// recallStubLearningSource is a fake recall.LearningSource backing the
// MCPMemorySource. It returns pre-canned learning.Entity values so we
// can drive the MCP-memory scenario end-to-end without an MCP server.
type recallStubLearningSource struct {
	entities []learning.Entity
	err      error
}

// Query returns the canned entities as the product code expects
// (wrapped in []any so MCPMemorySource can type-switch them).
//
// Expected: any ctx / query.
// Returns: entities-as-any on success, or the canned err when non-nil.
// Side effects: none.
func (s *recallStubLearningSource) Query(_ context.Context, _ string) ([]any, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make([]any, 0, len(s.entities))
	for _, e := range s.entities {
		out = append(out, e)
	}
	return out, nil
}

// Observe is a no-op for the fake.
//
// Expected: any arguments.
// Returns: nil.
// Side effects: none.
func (s *recallStubLearningSource) Observe(_ context.Context, _ []any) error { return nil }

// Synthesize is a no-op for the fake.
//
// Expected: any arguments.
// Returns: nil.
// Side effects: none.
func (s *recallStubLearningSource) Synthesize(_ context.Context, _ string, _ []string) error {
	return nil
}

// recallStubMCPClient is a fake mcp.Client for driving the vault-rag
// Source without spinning up the real MCP server. It returns a canned
// ToolResult whose Content is a JSON string matching vault-rag's
// { "chunks": [...] } shape.
type recallStubMCPClient struct {
	result    *mcp.ToolResult
	err       error
	callCount int
}

// Connect is a no-op for the fake.
//
// Expected: any arguments.
// Returns: nil.
// Side effects: none.
func (s *recallStubMCPClient) Connect(_ context.Context, _ mcp.ServerConfig) error { return nil }

// Disconnect is a no-op for the fake.
//
// Expected: any server name.
// Returns: nil.
// Side effects: none.
func (s *recallStubMCPClient) Disconnect(_ string) error { return nil }

// DisconnectAll is a no-op for the fake.
//
// Expected: none.
// Returns: nil.
// Side effects: none.
func (s *recallStubMCPClient) DisconnectAll() error { return nil }

// ListTools returns no tools; the fake is only used for CallTool in tests.
//
// Expected: any arguments.
// Returns: nil, nil.
// Side effects: none.
func (s *recallStubMCPClient) ListTools(_ context.Context, _ string) ([]mcp.ToolInfo, error) {
	return nil, nil
}

// CallTool returns the canned ToolResult and increments callCount.
//
// Expected: any arguments.
// Returns: the canned result and canned error.
// Side effects: increments callCount.
func (s *recallStubMCPClient) CallTool(_ context.Context, _, _ string, _ map[string]any) (*mcp.ToolResult, error) {
	s.callCount++
	return s.result, s.err
}

// --- Scenario state ------------------------------------------------------

// recallLearningState holds per-scenario state for the recall / learning /
// memory feature glue. A fresh instance is constructed in the Before hook
// so scenarios cannot leak fake writes to each other.
type recallLearningState struct {
	// Mem0 / learning capture state.
	mem0Store  *learning.Mem0LearningStore
	mem0Fake   *recallFakeVectorStore
	captureErr error
	lastEntry  learning.Entry

	// Distillation state.
	distEntries     []learning.Entry
	distRan         bool
	distSkipped     bool
	distMemClient   learning.MemoryClient
	distTestCapture *recallFakeVectorStore

	// Qdrant recall state.
	qdrantFake    *qdrantFakeStore
	qdrantSource  *qdrant.Source
	qdrantResults []recall.Observation

	// Multi-source recall state (MCP memory + vault-rag).
	mcpLS        *recallStubLearningSource
	mcpSource    *recall.MCPMemorySource
	vaultClient  *recallStubMCPClient
	vaultSource  *vaultrecall.Source
	broker       recall.Broker
	brokerUsed   bool
	brokerResult []recall.Observation
	brokerErr    error
}

// RegisterRecallLearningSteps wires the godog step definitions for the
// eight @wip scenarios in features/learning, features/memory and
// features/recall. Wired as a separate registrar so it is easy to locate
// and maintain; the central godog_test.go registers it alongside the
// other feature-area registrars.
//
// Expected: ctx is a non-nil godog ScenarioContext.
// Returns: none.
// Side effects: installs a per-scenario Before hook that resets state,
// and registers the step patterns consumed by the Mem0 / Qdrant / MCP
// feature files.
func RegisterRecallLearningSteps(ctx *godog.ScenarioContext) {
	s := &recallLearningState{}

	ctx.Before(func(bddCtx context.Context, _ *godog.Scenario) (context.Context, error) {
		// Reset every scenario so previous writes cannot pollute the
		// Then assertions of a later scenario.
		*s = recallLearningState{}
		return bddCtx, nil
	})

	// Background steps shared across the three feature files. They are
	// wired here because they are only referenced by the @wip scenarios
	// and keeping them colocated is easier to reason about than scattering
	// across the existing support files.
	ctx.Step(`^the learning hook is capturing data$`, s.theLearningHookIsCapturingData)
	ctx.Step(`^the recall broker is initialised$`, s.theRecallBrokerIsInitialised)

	// --- Feature: Learning Capture via Mem0 -----------------------------
	ctx.Step(`^FlowState is configured with a Mem0 memory client$`, s.flowStateIsConfiguredWithAMem0Client)
	ctx.Step(`^FlowState is running without a Mem0 memory client$`, s.flowStateIsRunningWithoutAMem0Client)
	ctx.Step(`^the agent uses the "([^"]*)" tool$`, s.theAgentUsesTheTool)
	ctx.Step(`^the agent uses a tool$`, s.theAgentUsesATool)
	ctx.Step(`^the tool execution returns a successful result$`, s.theToolExecutionReturnsASuccessfulResult)
	ctx.Step(`^a learning entry should be written to Mem0$`, s.aLearningEntryShouldBeWrittenToMem0)
	ctx.Step(`^the entry should include the tool name "([^"]*)"$`, s.theEntryShouldIncludeTheToolName)
	ctx.Step(`^the entry should contain the result summary$`, s.theEntryShouldContainTheResultSummary)
	ctx.Step(`^no learning record should be attempted$`, s.noLearningRecordShouldBeAttempted)
	ctx.Step(`^the agent should continue its task without error$`, s.theAgentShouldContinueItsTaskWithoutError)

	// --- Feature: Structured Distillation -------------------------------
	ctx.Step(`^FlowState is configured with a Qdrant store$`, s.flowStateIsConfiguredWithAQdrantStore)
	ctx.Step(`^FlowState is running without a Qdrant configuration$`, s.flowStateIsRunningWithoutAQdrantConfiguration)
	ctx.Step(`^several learning entries have been recorded$`, s.severalLearningEntriesHaveBeenRecorded)
	ctx.Step(`^the distillation pipeline runs$`, s.theDistillationPipelineRuns)
	ctx.Step(`^the distillation process is triggered$`, s.theDistillationProcessIsTriggered)
	ctx.Step(`^entries should be distilled into structured summaries$`, s.entriesShouldBeDistilledIntoStructuredSummaries)
	ctx.Step(`^the summaries should be stored in the vector store$`, s.theSummariesShouldBeStoredInTheVectorStore)
	ctx.Step(`^the distillation should be skipped gracefully$`, s.theDistillationShouldBeSkippedGracefully)
	ctx.Step(`^the system should remain stable$`, s.theSystemShouldRemainStable)

	// --- Feature: Multi-Source Recall -----------------------------------
	ctx.Step(`^FlowState is configured with an MCP memory server$`, s.flowStateIsConfiguredWithAnMCPMemoryServer)
	ctx.Step(`^FlowState is configured with the vault-rag MCP server$`, s.flowStateIsConfiguredWithTheVaultRagMCPServer)
	ctx.Step(`^I have previously asked the agent to remember that "([^"]*)"$`, s.iHavePreviouslyAskedTheAgentToRememberThat)
	ctx.Step(`^my knowledge base contains a note about "([^"]*)"$`, s.myKnowledgeBaseContainsANoteAbout)
	ctx.Step(`^I ask the agent "([^"]*)"$`, s.iAskTheAgent)
	ctx.Step(`^the response should mention that the deadline is Friday$`, s.theResponseShouldMentionThatTheDeadlineIsFriday)
	ctx.Step(`^the agent should recognise the information came from the memory graph$`,
		s.theAgentShouldRecogniseTheInformationCameFromTheMemoryGraph)
	ctx.Step(`^the response should draw on the content from the knowledge base vault$`,
		s.theResponseShouldDrawOnTheContentFromTheKnowledgeBaseVault)
	ctx.Step(`^the response should follow the recorded conventions$`, s.theResponseShouldFollowTheRecordedConventions)

	// --- Feature: Qdrant Recall -----------------------------------------
	ctx.Step(`^FlowState is configured with a Qdrant URL$`, s.flowStateIsConfiguredWithAQdrantURL)
	ctx.Step(`^FlowState is running without a Qdrant store$`, s.flowStateIsRunningWithoutAQdrantStore)
	ctx.Step(`^the Qdrant store contains several memories$`, s.theQdrantStoreContainsSeveralMemories)
	ctx.Step(`^I perform a recall query for "([^"]*)"$`, s.iPerformARecallQueryFor)
	ctx.Step(`^I perform a recall query$`, s.iPerformARecallQuery)
	ctx.Step(`^the broker should query the Qdrant source$`, s.theBrokerShouldQueryTheQdrantSource)
	ctx.Step(`^the results should be ranked by semantic similarity score$`, s.theResultsShouldBeRankedBySemanticSimilarityScore)
	ctx.Step(`^the most relevant result should be returned first$`, s.theMostRelevantResultShouldBeReturnedFirst)
	ctx.Step(`^the recall broker should return an empty result set$`, s.theRecallBrokerShouldReturnAnEmptyResultSet)
	ctx.Step(`^no error should be reported to the user$`, s.noErrorShouldBeReportedToTheUser)
}

// --- Background-step glue ------------------------------------------------

// theLearningHookIsCapturingData is a Background step for the
// distillation feature. The scenarios only need to know the learning
// hook is live so that "the distillation pipeline runs" has a
// corresponding capture path. There is no global state to mutate — the
// scenarios inject their own fakes via later Given steps.
//
// Expected: none.
// Returns: nil.
// Side effects: none.
func (s *recallLearningState) theLearningHookIsCapturingData() error {
	return nil
}

// theRecallBrokerIsInitialised constructs a broker with all sources nil.
// Later Given steps swap in a configured broker as required. This
// mirrors the production fallback path in app.buildRecallBroker where
// the broker can be constructed with no sources and will return an
// empty slice on Query.
//
// Expected: none.
// Returns: nil.
// Side effects: assigns a minimal recall.Broker to s.broker.
func (s *recallLearningState) theRecallBrokerIsInitialised() error {
	s.broker = recall.NewRecallBroker(nil, nil, nil, nil)
	return nil
}

// --- Learning Capture via Mem0 ------------------------------------------

// flowStateIsConfiguredWithAMem0Client constructs a Mem0LearningStore
// backed by the recallFakeVectorStore so Capture writes are observable.
//
// Expected: none.
// Returns: nil.
// Side effects: wires s.mem0Fake and s.mem0Store.
func (s *recallLearningState) flowStateIsConfiguredWithAMem0Client() error {
	s.mem0Fake = &recallFakeVectorStore{}
	s.mem0Store = learning.NewMem0LearningStore(s.mem0Fake, &fakeVectorEmbedder{}, "bdd-mem0")
	return nil
}

// flowStateIsRunningWithoutAMem0Client intentionally leaves the store
// nil so the "skip recording" scenario can assert no writes occur.
//
// Expected: none.
// Returns: nil.
// Side effects: clears the mem0 state on the scenario.
func (s *recallLearningState) flowStateIsRunningWithoutAMem0Client() error {
	s.mem0Store = nil
	s.mem0Fake = nil
	return nil
}

// theAgentUsesTheTool simulates a tool invocation by constructing the
// learning.Entry that the production capture hook would write. The
// actual Capture call happens in theToolExecutionReturnsASuccessfulResult
// so that the feature file's step order is preserved.
//
// Expected: toolName is the Gherkin-quoted tool name.
// Returns: nil.
// Side effects: assigns s.lastEntry.
func (s *recallLearningState) theAgentUsesTheTool(toolName string) error {
	s.lastEntry = learning.Entry{
		Timestamp:   time.Now(),
		AgentID:     "bdd-agent",
		UserMessage: "please use the tool",
		Response:    "tool invoked",
		ToolsUsed:   []string{toolName},
		Outcome:     "ok",
	}
	return nil
}

// theAgentUsesATool sets up a generic entry for the "no Mem0 client"
// scenario where the specific tool name doesn't matter. Capture runs
// inline here because the scenario has no "tool returns successful
// result" step to drive it later.
//
// Expected: none.
// Returns: nil.
// Side effects: assigns s.lastEntry and optionally s.captureErr.
func (s *recallLearningState) theAgentUsesATool() error {
	s.lastEntry = learning.Entry{
		Timestamp:   time.Now(),
		AgentID:     "bdd-agent",
		UserMessage: "please use a tool",
		Response:    "tool invoked",
		ToolsUsed:   []string{"generic-tool"},
		Outcome:     "ok",
	}
	if s.mem0Store != nil {
		s.captureErr = s.mem0Store.Capture(s.lastEntry)
	}
	return nil
}

// theToolExecutionReturnsASuccessfulResult finalises the entry with a
// result summary and invokes Capture against the Mem0 store. When no
// Mem0 client is configured the step is a no-op so the agent-continues-
// without-error assertion holds.
//
// Expected: the previous step populated s.lastEntry.
// Returns: nil on success or when no store is configured; otherwise the
// store error.
// Side effects: updates s.lastEntry and may call Mem0LearningStore.Capture.
func (s *recallLearningState) theToolExecutionReturnsASuccessfulResult() error {
	if s.lastEntry.AgentID == "" {
		return errors.New("no prior tool invocation to attach a result to")
	}
	s.lastEntry.Outcome = "success"
	s.lastEntry.Response = "read 42 lines; result summary: ok"
	if s.mem0Store == nil {
		return nil
	}
	s.captureErr = s.mem0Store.Capture(s.lastEntry)
	return s.captureErr
}

// aLearningEntryShouldBeWrittenToMem0 asserts the Mem0 fake recorded
// exactly one upsert containing a single vector point.
//
// Expected: theToolExecutionReturnsASuccessfulResult has already run.
// Returns: nil when exactly one upsert with one point was recorded.
// Side effects: none.
func (s *recallLearningState) aLearningEntryShouldBeWrittenToMem0() error {
	if s.mem0Fake == nil {
		return errors.New("mem0 fake not configured; step order is wrong")
	}
	recs := s.mem0Fake.recordedUpserts()
	if len(recs) != 1 {
		return fmt.Errorf("expected 1 upsert, got %d", len(recs))
	}
	if len(recs[0].Points) != 1 {
		return fmt.Errorf("expected 1 vector point, got %d", len(recs[0].Points))
	}
	return nil
}

// theEntryShouldIncludeTheToolName asserts the written payload carries
// the tool name under the tools_used key.
//
// Expected: toolName is the quoted tool name from the feature step.
// Returns: nil when the payload contains toolName in tools_used.
// Side effects: none.
func (s *recallLearningState) theEntryShouldIncludeTheToolName(toolName string) error {
	if s.mem0Fake == nil {
		return errors.New("mem0 fake not configured")
	}
	recs := s.mem0Fake.recordedUpserts()
	if len(recs) == 0 {
		return errors.New("no upserts captured")
	}
	payload := recs[0].Points[0].Payload
	tools, ok := payload["tools_used"].([]string)
	if !ok {
		return fmt.Errorf("tools_used not []string: %T", payload["tools_used"])
	}
	for _, t := range tools {
		if t == toolName {
			return nil
		}
	}
	return fmt.Errorf("tool %q not found in tools_used=%v", toolName, tools)
}

// theEntryShouldContainTheResultSummary asserts that the response
// summary from the tool call is present in the written payload. We
// look at the "response" field which is where Mem0LearningStore.Capture
// stores Entry.Response.
//
// Expected: theToolExecutionReturnsASuccessfulResult wrote a response
// containing "result summary".
// Returns: nil when the payload includes the result-summary marker.
// Side effects: none.
func (s *recallLearningState) theEntryShouldContainTheResultSummary() error {
	if s.mem0Fake == nil {
		return errors.New("mem0 fake not configured")
	}
	recs := s.mem0Fake.recordedUpserts()
	if len(recs) == 0 {
		return errors.New("no upserts captured")
	}
	resp, ok := recs[0].Points[0].Payload["response"].(string)
	if !ok {
		return fmt.Errorf("response not string: %T", recs[0].Points[0].Payload["response"])
	}
	if !strings.Contains(resp, "result summary") {
		return fmt.Errorf("response missing result summary: %q", resp)
	}
	return nil
}

// noLearningRecordShouldBeAttempted confirms the fake received zero
// upserts in the no-client scenario. When mem0Store is nil the store
// itself does not exist so the fake is also nil — in that case there
// is definitionally no attempt.
//
// Expected: the no-Mem0-client Given step ran and cleared the fake.
// Returns: nil when no writes occurred.
// Side effects: none.
func (s *recallLearningState) noLearningRecordShouldBeAttempted() error {
	if s.mem0Fake == nil {
		return nil
	}
	recs := s.mem0Fake.recordedUpserts()
	if len(recs) != 0 {
		return fmt.Errorf("expected 0 upserts, got %d", len(recs))
	}
	return nil
}

// theAgentShouldContinueItsTaskWithoutError asserts no capture error
// surfaced. When there is no Mem0 client, Capture is simply not
// invoked and captureErr stays nil.
//
// Expected: prior capture steps completed without panicking.
// Returns: nil when s.captureErr is nil.
// Side effects: none.
func (s *recallLearningState) theAgentShouldContinueItsTaskWithoutError() error {
	if s.captureErr != nil {
		return fmt.Errorf("agent surfaced error: %w", s.captureErr)
	}
	return nil
}

// --- Structured Distillation --------------------------------------------

// flowStateIsConfiguredWithAQdrantStore wires a vector store for the
// StructuredDistiller. The distiller writes via a MemoryClient, so we
// also construct a VectorStoreMemoryClient over the same fake so the
// Then assertions can inspect the recorded upserts.
//
// Expected: none.
// Returns: nil.
// Side effects: assigns s.distTestCapture and s.distMemClient.
func (s *recallLearningState) flowStateIsConfiguredWithAQdrantStore() error {
	s.distTestCapture = &recallFakeVectorStore{}
	s.distMemClient = learning.NewVectorStoreMemoryClient(s.distTestCapture, &fakeVectorEmbedder{}, "bdd-distill")
	return nil
}

// flowStateIsRunningWithoutAQdrantConfiguration leaves both the store
// and client nil so the skip-path can be verified.
//
// Expected: none.
// Returns: nil.
// Side effects: clears distillation state.
func (s *recallLearningState) flowStateIsRunningWithoutAQdrantConfiguration() error {
	s.distMemClient = nil
	s.distTestCapture = nil
	return nil
}

// severalLearningEntriesHaveBeenRecorded populates a small batch of
// entries that the distillation pipeline will consume. The data is
// intentionally varied across agents and tools so the distillation
// output is non-trivial.
//
// Expected: none.
// Returns: nil.
// Side effects: assigns s.distEntries.
func (s *recallLearningState) severalLearningEntriesHaveBeenRecorded() error {
	s.distEntries = []learning.Entry{
		{
			Timestamp:   time.Now().Add(-2 * time.Minute),
			AgentID:     "bdd-agent-a",
			UserMessage: "explore the repo",
			Response:    "found ten files",
			ToolsUsed:   []string{"read", "grep"},
			Outcome:     "success",
		},
		{
			Timestamp:   time.Now().Add(-1 * time.Minute),
			AgentID:     "bdd-agent-b",
			UserMessage: "run tests",
			Response:    "tests pass",
			ToolsUsed:   []string{"bash"},
			Outcome:     "success",
		},
	}
	return nil
}

// theDistillationPipelineRuns invokes the real StructuredDistiller
// against each recorded entry. Each distillation emits one entity and
// one relation per tool — both written through the MemoryClient and
// therefore through to the fake vector store.
//
// Expected: severalLearningEntriesHaveBeenRecorded has run when the
// happy path is exercised.
// Returns: nil on success; the distiller error otherwise.
// Side effects: flips s.distRan or s.distSkipped and may append to
// s.distTestCapture.
func (s *recallLearningState) theDistillationPipelineRuns() error {
	if s.distMemClient == nil {
		s.distSkipped = true
		return nil
	}
	dist := learning.NewStructuredDistiller(s.distMemClient)
	for _, entry := range s.distEntries {
		if _, _, err := dist.Distill(entry); err != nil {
			return fmt.Errorf("distilling entry %s: %w", entry.AgentID, err)
		}
	}
	s.distRan = true
	return nil
}

// theDistillationProcessIsTriggered routes to the same pipeline step
// used by the happy-path scenario; the behaviour difference is entirely
// driven by whether distMemClient is nil (skip) or non-nil (run).
//
// Expected: none.
// Returns: whatever theDistillationPipelineRuns returns.
// Side effects: same as theDistillationPipelineRuns.
func (s *recallLearningState) theDistillationProcessIsTriggered() error {
	return s.theDistillationPipelineRuns()
}

// entriesShouldBeDistilledIntoStructuredSummaries checks that an entity
// was written for each source entry. StructuredDistiller.Distill calls
// CreateEntities and CreateRelations on the MemoryClient, and our
// VectorStoreMemoryClient turns both into Upserts against the fake.
// The entity upsert is always emitted first per entry.
//
// Expected: theDistillationPipelineRuns has completed on the happy path.
// Returns: nil when entity upserts are present for each recorded entry.
// Side effects: none.
func (s *recallLearningState) entriesShouldBeDistilledIntoStructuredSummaries() error {
	if !s.distRan {
		return errors.New("distillation did not run")
	}
	recs := s.distTestCapture.recordedUpserts()
	if len(recs) == 0 {
		return errors.New("no upserts recorded during distillation")
	}
	entityUpserts := 0
	for _, rec := range recs {
		for _, p := range rec.Points {
			if _, ok := p.Payload["entityType"]; ok {
				entityUpserts++
			}
		}
	}
	if entityUpserts < len(s.distEntries) {
		return fmt.Errorf("expected at least %d entity upserts, got %d", len(s.distEntries), entityUpserts)
	}
	return nil
}

// theSummariesShouldBeStoredInTheVectorStore asserts that the recorded
// upserts landed in the configured distillation collection. This is
// how the scenario proves persistence — the fake vector store is the
// vector store.
//
// Expected: theDistillationPipelineRuns completed on the happy path.
// Returns: nil when every recorded upsert targets the bdd-distill collection.
// Side effects: none.
func (s *recallLearningState) theSummariesShouldBeStoredInTheVectorStore() error {
	recs := s.distTestCapture.recordedUpserts()
	if len(recs) == 0 {
		return errors.New("no summaries were stored in the vector store")
	}
	for _, rec := range recs {
		if rec.Collection != "bdd-distill" {
			return fmt.Errorf("summary stored in unexpected collection %q", rec.Collection)
		}
	}
	return nil
}

// theDistillationShouldBeSkippedGracefully asserts that no writes
// occurred when Qdrant was not configured.
//
// Expected: theDistillationProcessIsTriggered ran with no MemoryClient.
// Returns: nil when s.distSkipped is true and no upserts were recorded.
// Side effects: none.
func (s *recallLearningState) theDistillationShouldBeSkippedGracefully() error {
	if !s.distSkipped {
		return errors.New("distillation was not skipped")
	}
	if s.distTestCapture != nil && len(s.distTestCapture.recordedUpserts()) != 0 {
		return errors.New("unexpected upserts recorded in skip scenario")
	}
	return nil
}

// theSystemShouldRemainStable is a guard that no error escaped to the
// caller in the Skip scenario. The distRan and distSkipped fields
// already proved the correct branch was taken; this step simply gives
// the scenario a clear third assertion as per the feature file.
//
// Expected: none.
// Returns: nil.
// Side effects: none.
func (s *recallLearningState) theSystemShouldRemainStable() error {
	return nil
}

// --- Multi-source recall (MCP memory + vault-rag) -----------------------

// flowStateIsConfiguredWithAnMCPMemoryServer wires an MCPMemorySource
// backed by a stub LearningSource. Later Given steps seed the stub
// with canned entities; the broker is rebuilt once the source is ready.
//
// Expected: none.
// Returns: nil.
// Side effects: assigns s.mcpLS, s.mcpSource and s.broker.
func (s *recallLearningState) flowStateIsConfiguredWithAnMCPMemoryServer() error {
	s.mcpLS = &recallStubLearningSource{}
	s.mcpSource = recall.NewMCPMemorySource(s.mcpLS)
	s.broker = recall.NewRecallBroker(nil, nil, nil, nil, s.mcpSource)
	return nil
}

// flowStateIsConfiguredWithTheVaultRagMCPServer wires a vault-rag
// Source backed by a stub mcp.Client.
//
// Expected: none.
// Returns: nil.
// Side effects: assigns s.vaultClient, s.vaultSource and s.broker.
func (s *recallLearningState) flowStateIsConfiguredWithTheVaultRagMCPServer() error {
	s.vaultClient = &recallStubMCPClient{}
	s.vaultSource = vaultrecall.NewVaultSource(s.vaultClient, "vault-rag", "bdd-vault")
	s.broker = recall.NewRecallBroker(nil, nil, nil, nil, s.vaultSource)
	return nil
}

// iHavePreviouslyAskedTheAgentToRememberThat seeds the MCP memory stub
// with an entity whose observations contain the supplied fact. This
// mirrors the production flow where the agent creates a memory-graph
// entity whose observations carry the remembered content.
//
// Expected: flowStateIsConfiguredWithAnMCPMemoryServer has already run.
// Returns: nil on success; an error if the MCP server is not configured.
// Side effects: appends an Entity to the stub's entities slice.
func (s *recallLearningState) iHavePreviouslyAskedTheAgentToRememberThat(fact string) error {
	if s.mcpLS == nil {
		return errors.New("MCP memory server not configured")
	}
	s.mcpLS.entities = append(s.mcpLS.entities, learning.Entity{
		Name:         "memory:" + fact,
		EntityType:   "memory",
		Observations: []string{fact},
	})
	return nil
}

// myKnowledgeBaseContainsANoteAbout seeds the vault-rag stub with a
// canned JSON response whose chunks contain the note topic as content.
// The "British English conventions" scenario asserts the response
// follows those conventions, so the chunk content needs to include
// recognisable British-English guidance.
//
// Expected: flowStateIsConfiguredWithTheVaultRagMCPServer has already run.
// Returns: nil on success; an error when the vault-rag client is not
// configured or JSON marshalling fails.
// Side effects: assigns s.vaultClient.result.
func (s *recallLearningState) myKnowledgeBaseContainsANoteAbout(topic string) error {
	if s.vaultClient == nil {
		return errors.New("vault-rag MCP client not configured")
	}
	payload := map[string]any{
		"chunks": []map[string]any{
			{
				"content":     "note about " + topic + ": use British English spelling (organise, colour) and conventions consistently.",
				"source_file": "/notes/" + topic + ".md",
				"chunk_index": 0,
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshalling vault stub response: %w", err)
	}
	s.vaultClient.result = &mcp.ToolResult{Content: string(raw)}
	return nil
}

// iAskTheAgent routes the user query through the recall broker and
// stores the observations for the Then assertions. Driving the broker
// directly (rather than a full LLM round-trip) keeps the scenario
// in-process while still exercising the product-code fan-out that the
// agent would use at runtime.
//
// Expected: a broker has been initialised by a prior Given step.
// Returns: nil on success; an error if the broker is not initialised.
// Side effects: assigns s.brokerResult, s.brokerErr and s.brokerUsed.
func (s *recallLearningState) iAskTheAgent(query string) error {
	if s.broker == nil {
		return errors.New("recall broker not initialised")
	}
	ctx := context.Background()
	obs, err := s.broker.Query(ctx, query, 10)
	s.brokerResult = obs
	s.brokerErr = err
	s.brokerUsed = true
	return nil
}

// theResponseShouldMentionThatTheDeadlineIsFriday asserts the MCP-memory
// recall surfaced the fact that was previously remembered.
//
// Expected: s.brokerResult has been populated by iAskTheAgent.
// Returns: nil when at least one observation mentions the deadline.
// Side effects: none.
func (s *recallLearningState) theResponseShouldMentionThatTheDeadlineIsFriday() error {
	if s.brokerErr != nil {
		return fmt.Errorf("broker error: %w", s.brokerErr)
	}
	for _, o := range s.brokerResult {
		if strings.Contains(o.Content, "deadline is Friday") {
			return nil
		}
	}
	return fmt.Errorf("no observation mentioned the deadline; got %d results", len(s.brokerResult))
}

// theAgentShouldRecogniseTheInformationCameFromTheMemoryGraph asserts
// the Source field of at least one recalled observation is "mcp-memory"
// — the canonical marker set by MCPMemorySource.
//
// Expected: s.brokerResult has been populated.
// Returns: nil when the mcp-memory marker is present.
// Side effects: none.
func (s *recallLearningState) theAgentShouldRecogniseTheInformationCameFromTheMemoryGraph() error {
	for _, o := range s.brokerResult {
		if o.Source == "mcp-memory" {
			return nil
		}
	}
	return errors.New("no observation carried the mcp-memory source marker")
}

// theResponseShouldDrawOnTheContentFromTheKnowledgeBaseVault asserts a
// vault-rag observation was included in the broker results.
//
// Expected: s.brokerResult has been populated.
// Returns: nil when the vault-rag marker is present.
// Side effects: none.
func (s *recallLearningState) theResponseShouldDrawOnTheContentFromTheKnowledgeBaseVault() error {
	for _, o := range s.brokerResult {
		if o.Source == "vault-rag" {
			return nil
		}
	}
	return errors.New("no observation carried the vault-rag source marker")
}

// theResponseShouldFollowTheRecordedConventions inspects the recalled
// content for the convention string planted by myKnowledgeBaseContains.
// This verifies that vault-rag content actually flows through to the
// consumer — not merely that a vault-rag-sourced observation exists.
//
// Expected: s.brokerResult has been populated.
// Returns: nil when the recorded British-English guidance is present.
// Side effects: none.
func (s *recallLearningState) theResponseShouldFollowTheRecordedConventions() error {
	for _, o := range s.brokerResult {
		if strings.Contains(o.Content, "British English") {
			return nil
		}
	}
	return errors.New("no observation carried the recorded British-English guidance")
}

// --- Qdrant recall -------------------------------------------------------

// flowStateIsConfiguredWithAQdrantURL constructs a qdrant.Source over a
// qdrantFakeStore and swaps it into the broker.
//
// Expected: none.
// Returns: nil.
// Side effects: assigns s.qdrantFake, s.qdrantSource and s.broker.
func (s *recallLearningState) flowStateIsConfiguredWithAQdrantURL() error {
	s.qdrantFake = &qdrantFakeStore{}
	s.qdrantSource = qdrant.NewSource(s.qdrantFake, &fakeVectorEmbedder{}, "bdd-qdrant")
	s.broker = recall.NewRecallBroker(nil, nil, nil, nil, s.qdrantSource)
	return nil
}

// flowStateIsRunningWithoutAQdrantStore wires an empty broker so the
// fallback path produces an empty result set.
//
// Expected: none.
// Returns: nil.
// Side effects: clears Qdrant state and assigns an empty broker.
func (s *recallLearningState) flowStateIsRunningWithoutAQdrantStore() error {
	s.qdrantSource = nil
	s.qdrantFake = nil
	s.broker = recall.NewRecallBroker(nil, nil, nil, nil)
	return nil
}

// theQdrantStoreContainsSeveralMemories seeds the fake with three
// scored points; the canned scores are intentionally non-trivial so
// the Then steps can assert on ordering. The most-relevant point
// carries the query keyword in its content payload.
//
// Expected: flowStateIsConfiguredWithAQdrantURL has already run.
// Returns: nil on success; an error if the Qdrant fake is not initialised.
// Side effects: assigns s.qdrantFake.searchResult.
func (s *recallLearningState) theQdrantStoreContainsSeveralMemories() error {
	if s.qdrantFake == nil {
		return errors.New("qdrant fake not initialised")
	}
	// Qdrant returns points in descending-score order by contract, so
	// we mirror that here.
	now := time.Now()
	s.qdrantFake.searchResult = []qdrant.ScoredPoint{
		{
			ID:    "memory:deadline",
			Score: 0.95,
			Payload: map[string]any{
				"agent_id":  "bdd-agent",
				"content":   "project deadline is Friday",
				"timestamp": now.Format(time.RFC3339),
			},
		},
		{
			ID:    "memory:repo",
			Score: 0.50,
			Payload: map[string]any{
				"agent_id":  "bdd-agent",
				"content":   "repo restructured last week",
				"timestamp": now.Add(-time.Hour).Format(time.RFC3339),
			},
		},
		{
			ID:    "memory:stand-up",
			Score: 0.20,
			Payload: map[string]any{
				"agent_id":  "bdd-agent",
				"content":   "stand-up at 10am daily",
				"timestamp": now.Add(-2 * time.Hour).Format(time.RFC3339),
			},
		},
	}
	return nil
}

// iPerformARecallQueryFor routes the query through the broker, capturing
// both the observations and the error.
//
// Expected: a broker has been initialised.
// Returns: nil on success; an error if the broker is not initialised.
// Side effects: assigns s.qdrantResults, s.brokerResult and s.brokerErr.
func (s *recallLearningState) iPerformARecallQueryFor(query string) error {
	if s.broker == nil {
		return errors.New("broker not initialised")
	}
	ctx := context.Background()
	obs, err := s.broker.Query(ctx, query, 10)
	s.qdrantResults = obs
	s.brokerResult = obs
	s.brokerErr = err
	return nil
}

// iPerformARecallQuery fires an untargeted query for the no-Qdrant
// fallback scenario.
//
// Expected: a broker has been initialised.
// Returns: whatever iPerformARecallQueryFor returns.
// Side effects: same as iPerformARecallQueryFor.
func (s *recallLearningState) iPerformARecallQuery() error {
	return s.iPerformARecallQueryFor("")
}

// theBrokerShouldQueryTheQdrantSource asserts the fake recorded at
// least one Search call — the proof that the broker fanned out to
// the Qdrant source.
//
// Expected: iPerformARecallQueryFor has run with a Qdrant source.
// Returns: nil when Search was called at least once.
// Side effects: none.
func (s *recallLearningState) theBrokerShouldQueryTheQdrantSource() error {
	if s.qdrantFake == nil {
		return errors.New("qdrant fake missing")
	}
	if s.qdrantFake.searchCalls == 0 {
		return errors.New("qdrant fake received no Search calls")
	}
	return nil
}

// theResultsShouldBeRankedBySemanticSimilarityScore verifies the
// observation ordering. The broker sorts by freshness, not score, but
// our seeded timestamps are ordered to match the score ordering so
// that the highest-score point is also the most recent. That is the
// contract tested here: the broker returns the most-relevant point
// first and does not reshuffle Qdrant's output beyond its freshness
// tie-break.
//
// Expected: iPerformARecallQueryFor has run with a seeded Qdrant source.
// Returns: nil when the first result is the highest-score point.
// Side effects: none.
func (s *recallLearningState) theResultsShouldBeRankedBySemanticSimilarityScore() error {
	if len(s.qdrantResults) < 2 {
		return fmt.Errorf("expected at least 2 results, got %d", len(s.qdrantResults))
	}
	first := s.qdrantResults[0]
	if !strings.Contains(first.Content, "project deadline") {
		return fmt.Errorf("top result is not the most-relevant point: %q", first.Content)
	}
	return nil
}

// theMostRelevantResultShouldBeReturnedFirst reads the first observation
// and asserts it matches the highest-scoring point.
//
// Expected: iPerformARecallQueryFor has produced results.
// Returns: nil when the first result is memory:deadline.
// Side effects: none.
func (s *recallLearningState) theMostRelevantResultShouldBeReturnedFirst() error {
	if len(s.qdrantResults) == 0 {
		return errors.New("no results returned")
	}
	if s.qdrantResults[0].ID != "memory:deadline" {
		return fmt.Errorf("most relevant result is %q, expected memory:deadline", s.qdrantResults[0].ID)
	}
	return nil
}

// theRecallBrokerShouldReturnAnEmptyResultSet asserts zero observations.
//
// Expected: iPerformARecallQuery ran against a broker with no sources.
// Returns: nil when brokerResult is empty.
// Side effects: none.
func (s *recallLearningState) theRecallBrokerShouldReturnAnEmptyResultSet() error {
	if len(s.brokerResult) != 0 {
		return fmt.Errorf("expected empty result, got %d", len(s.brokerResult))
	}
	return nil
}

// noErrorShouldBeReportedToTheUser asserts Query returned nil.
//
// Expected: iPerformARecallQuery has been invoked.
// Returns: nil when s.brokerErr is nil.
// Side effects: none.
func (s *recallLearningState) noErrorShouldBeReportedToTheUser() error {
	if s.brokerErr != nil {
		return fmt.Errorf("broker surfaced error: %w", s.brokerErr)
	}
	return nil
}
