// Package engine tests verify the complete recall pipeline from learning capture
// through distillation, broker query, and context window assembly.
// These tests prove that recalled information flows end-to-end through the system.
package engine_test

import (
	stdctx "context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/plugin"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

type pipelineTokenCounter struct {
	countFn      func(string) int
	modelLimitFn func(string) int
}

func (p *pipelineTokenCounter) Count(text string) int {
	if p.countFn != nil {
		return p.countFn(text)
	}
	return len(text) / 4
}

func (p *pipelineTokenCounter) ModelLimit(model string) int {
	if p.modelLimitFn != nil {
		return p.modelLimitFn(model)
	}
	return 8192
}

type pipelineProvider struct{}

func (p *pipelineProvider) Name() string { return "pipeline-provider" }
func (p *pipelineProvider) Stream(_ stdctx.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 1)
	ch <- provider.StreamChunk{Done: true}
	close(ch)
	return ch, nil
}
func (p *pipelineProvider) Chat(_ stdctx.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}
func (p *pipelineProvider) Embed(_ stdctx.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}
func (p *pipelineProvider) Models() ([]provider.Model, error) { return nil, nil }

type inMemoryKnowledgeGraph struct {
	entities           []learning.Entity
	relations          []learning.Relation
	createRelationsErr error
}

func (g *inMemoryKnowledgeGraph) CreateEntities(_ stdctx.Context, entities []learning.Entity) ([]learning.Entity, error) {
	g.entities = append(g.entities, entities...)
	return entities, nil
}

func (g *inMemoryKnowledgeGraph) CreateRelations(_ stdctx.Context, relations []learning.Relation) ([]learning.Relation, error) {
	if g.createRelationsErr != nil {
		return nil, g.createRelationsErr
	}
	g.relations = append(g.relations, relations...)
	return relations, nil
}

func (g *inMemoryKnowledgeGraph) AddObservations(_ stdctx.Context, obs []learning.ObservationEntry) ([]learning.ObservationEntry, error) {
	for _, o := range obs {
		for i := range g.entities {
			if g.entities[i].Name == o.EntityName {
				g.entities[i].Observations = append(g.entities[i].Observations, o.Contents...)
			}
		}
	}
	return obs, nil
}

func (g *inMemoryKnowledgeGraph) SearchNodes(_ stdctx.Context, query string) ([]learning.Entity, error) {
	queryWords := strings.Fields(strings.ToLower(query))
	var results []learning.Entity
	for _, e := range g.entities {
		if g.entityMatchesAny(e, queryWords) {
			results = append(results, e)
		}
	}
	return results, nil
}

func (g *inMemoryKnowledgeGraph) entityMatchesAny(e learning.Entity, words []string) bool {
	for _, obs := range e.Observations {
		lower := strings.ToLower(obs)
		for _, word := range words {
			if len(word) > 2 && strings.Contains(lower, word) {
				return true
			}
		}
	}
	return false
}

func (g *inMemoryKnowledgeGraph) OpenNodes(_ stdctx.Context, names []string) (learning.KnowledgeGraph, error) {
	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}
	var entities []learning.Entity
	for _, e := range g.entities {
		if nameSet[e.Name] {
			entities = append(entities, e)
		}
	}
	return learning.KnowledgeGraph{Entities: entities, Relations: g.relations}, nil
}

func (g *inMemoryKnowledgeGraph) DeleteEntities(_ stdctx.Context, names []string) ([]string, error) {
	return names, nil
}
func (g *inMemoryKnowledgeGraph) DeleteObservations(_ stdctx.Context, _ []learning.DeletionEntry) error {
	return nil
}
func (g *inMemoryKnowledgeGraph) DeleteRelations(_ stdctx.Context, _ []learning.Relation) error {
	return nil
}
func (g *inMemoryKnowledgeGraph) ReadGraph(_ stdctx.Context) (learning.KnowledgeGraph, error) {
	return learning.KnowledgeGraph{Entities: g.entities, Relations: g.relations}, nil
}
func (g *inMemoryKnowledgeGraph) WriteLearningRecord(_ *learning.Record) error { return nil }

type knowledgeGraphRecallSource struct {
	graph   *inMemoryKnowledgeGraph
	agentID string
}

func (s *knowledgeGraphRecallSource) Query(ctx stdctx.Context, query string, limit int) ([]recall.Observation, error) {
	entities, err := s.graph.SearchNodes(ctx, query)
	if err != nil {
		return nil, err
	}
	var observations []recall.Observation
	for _, entity := range entities {
		for _, obs := range entity.Observations {
			observations = append(observations, recall.Observation{
				ID:        entity.Name + "-" + obs[:min(len(obs), 10)],
				Source:    "knowledge-graph",
				AgentID:   s.agentID,
				Content:   obs,
				Timestamp: time.Now(),
			})
		}
	}
	if limit > 0 && len(observations) > limit {
		observations = observations[:limit]
	}
	return observations, nil
}

var _ = Describe("Recall Pipeline Integration", Label("integration", "recall-pipeline"), func() {
	var (
		graph     *inMemoryKnowledgeGraph
		distiller *learning.StructuredDistiller
		store     *learning.JSONFileStore
		tmpDir    string
		manifest  agent.Manifest
	)

	BeforeEach(func() {
		graph = &inMemoryKnowledgeGraph{}
		distiller = learning.NewStructuredDistiller(graph)
		var err error
		tmpDir, err = os.MkdirTemp("", "recall-pipeline-test")
		Expect(err).NotTo(HaveOccurred())
		store = learning.NewJSONFileStore(filepath.Join(tmpDir, "learning.json"))
		manifest = agent.Manifest{
			ID:   "test-agent",
			Name: "Test Agent",
			Instructions: agent.Instructions{
				SystemPrompt: "You are a test agent.",
			},
		}
	})

	AfterEach(func() {
		os.RemoveAll(tmpDir)
	})

	Describe("Full Pipeline: capture → distill → recall → context", func() {
		It("recalled knowledge appears in the context window", func() {
			entry := learning.Entry{
				Timestamp:   time.Now().Add(-10 * time.Minute),
				AgentID:     "test-agent",
				UserMessage: "How do I implement error handling in Go?",
				Response:    "Use sentinel errors and wrap with fmt.Errorf",
				ToolsUsed:   []string{"editor"},
				Outcome:     "success",
			}

			Expect(store.Capture(entry)).To(Succeed())

			entity, relations, err := distiller.Distill(entry)
			Expect(err).NotTo(HaveOccurred())
			Expect(entity.Observations).To(HaveLen(6))
			Expect(relations).To(HaveLen(1))

			recallSource := &knowledgeGraphRecallSource{graph: graph, agentID: "test-agent"}
			broker := recall.NewRecallBroker(nil, nil, nil, recallSource)

			contextStore, err := recall.NewFileContextStore(
				filepath.Join(tmpDir, "context.json"), "test-model",
			)
			Expect(err).NotTo(HaveOccurred())

			eng := engine.New(engine.Config{
				ChatProvider: &pipelineProvider{},
				Manifest:     manifest,
				TokenCounter: &pipelineTokenCounter{},
				Store:        contextStore,
				RecallBroker: broker,
			})

			msgs := eng.BuildContextWindowForTest(
				stdctx.Background(), "ses-1", "Tell me about error handling",
			)

			contents := collectContents(msgs)
			Expect(contents).To(ContainElement(ContainSubstring("error handling")))

			lastMsg := msgs[len(msgs)-1]
			Expect(lastMsg.Role).To(Equal("user"))
			Expect(lastMsg.Content).To(Equal("Tell me about error handling"))
		})
	})

	Describe("Building complete context with multiple recalled observations", func() {
		It("assembles messages in system → observations → user order", func() {
			entries := []learning.Entry{
				{
					Timestamp:   time.Now().Add(-30 * time.Minute),
					AgentID:     "test-agent",
					UserMessage: "What is the repository pattern?",
					Response:    "Repository pattern abstracts data access behind an interface",
					ToolsUsed:   []string{"editor"},
					Outcome:     "success",
				},
				{
					Timestamp:   time.Now().Add(-15 * time.Minute),
					AgentID:     "test-agent",
					UserMessage: "How do I structure Go packages?",
					Response:    "Group by domain, keep packages small and focused",
					ToolsUsed:   []string{"editor", "terminal"},
					Outcome:     "success",
				},
			}

			for _, entry := range entries {
				Expect(store.Capture(entry)).To(Succeed())
				_, _, err := distiller.Distill(entry)
				Expect(err).NotTo(HaveOccurred())
			}

			recallSource := &knowledgeGraphRecallSource{graph: graph, agentID: "test-agent"}
			broker := recall.NewRecallBroker(nil, nil, nil, recallSource)

			contextStore, err := recall.NewFileContextStore(
				filepath.Join(tmpDir, "context.json"), "test-model",
			)
			Expect(err).NotTo(HaveOccurred())

			eng := engine.New(engine.Config{
				ChatProvider: &pipelineProvider{},
				Manifest:     manifest,
				TokenCounter: &pipelineTokenCounter{},
				Store:        contextStore,
				RecallBroker: broker,
			})

			msgs := eng.BuildContextWindowForTest(
				stdctx.Background(), "ses-1", "Tell me about repository pattern and packages",
			)

			Expect(len(msgs)).To(BeNumerically(">=", 3))
			Expect(msgs[0].Role).To(Equal("system"))
			Expect(msgs[0].Content).To(ContainSubstring("You are a test agent"))

			Expect(msgs[len(msgs)-1].Role).To(Equal("user"))
			Expect(msgs[len(msgs)-1].Content).To(Equal("Tell me about repository pattern and packages"))

			for i := 1; i < len(msgs)-1; i++ {
				Expect(msgs[i].Role).To(Equal("assistant"))
			}

			contents := collectContents(msgs)
			Expect(contents).To(ContainElement(ContainSubstring("repository")))
			Expect(contents).To(ContainElement(ContainSubstring("packages")))
		})
	})

	Describe("Context updates with recently added relevant information", func() {
		It("new knowledge captured between queries appears in subsequent context builds", func() {
			initialEntry := learning.Entry{
				Timestamp:   time.Now().Add(-20 * time.Minute),
				AgentID:     "test-agent",
				UserMessage: "What is dependency injection?",
				Response:    "DI passes dependencies via constructors instead of creating them internally",
				ToolsUsed:   []string{"editor"},
				Outcome:     "success",
			}
			Expect(store.Capture(initialEntry)).To(Succeed())
			_, _, err := distiller.Distill(initialEntry)
			Expect(err).NotTo(HaveOccurred())

			recallSource := &knowledgeGraphRecallSource{graph: graph, agentID: "test-agent"}
			broker := recall.NewRecallBroker(nil, nil, nil, recallSource)
			contextStore, err := recall.NewFileContextStore(
				filepath.Join(tmpDir, "context.json"), "test-model",
			)
			Expect(err).NotTo(HaveOccurred())

			eng := engine.New(engine.Config{
				ChatProvider: &pipelineProvider{},
				Manifest:     manifest,
				TokenCounter: &pipelineTokenCounter{},
				Store:        contextStore,
				RecallBroker: broker,
			})

			firstMsgs := eng.BuildContextWindowForTest(
				stdctx.Background(), "ses-1", "Tell me about testing patterns",
			)
			firstContents := collectContents(firstMsgs)
			Expect(firstContents).NotTo(ContainElement(ContainSubstring("table-driven")))

			newEntry := learning.Entry{
				Timestamp:   time.Now(),
				AgentID:     "test-agent",
				UserMessage: "What are table-driven tests?",
				Response:    "Table-driven tests use a slice of test cases with inputs and expected outputs",
				ToolsUsed:   []string{"editor"},
				Outcome:     "success",
			}
			Expect(store.Capture(newEntry)).To(Succeed())
			_, _, err = distiller.Distill(newEntry)
			Expect(err).NotTo(HaveOccurred())

			secondMsgs := eng.BuildContextWindowForTest(
				stdctx.Background(), "ses-2", "Tell me about table-driven tests",
			)
			secondContents := collectContents(secondMsgs)
			Expect(secondContents).To(ContainElement(ContainSubstring("table-driven")))
		})

		It("enriches context via custom hooks alongside broker observations", func() {
			entry := learning.Entry{
				Timestamp:   time.Now().Add(-5 * time.Minute),
				AgentID:     "test-agent",
				UserMessage: "What is the observer pattern?",
				Response:    "Observer pattern notifies dependents of state changes",
				ToolsUsed:   []string{"editor"},
				Outcome:     "success",
			}
			Expect(store.Capture(entry)).To(Succeed())
			_, _, err := distiller.Distill(entry)
			Expect(err).NotTo(HaveOccurred())

			recallSource := &knowledgeGraphRecallSource{graph: graph, agentID: "test-agent"}
			broker := recall.NewRecallBroker(nil, nil, nil, recallSource)

			realtimeHook := func(_ stdctx.Context, payload *plugin.ContextAssemblyPayload) error {
				payload.SearchResults = append(payload.SearchResults, recall.SearchResult{
					MessageID: "realtime-1",
					Score:     1.0,
					Message: provider.Message{
						Role:    "assistant",
						Content: "Recent session note: user prefers event-driven architecture",
					},
				})
				return nil
			}

			contextStore, err := recall.NewFileContextStore(
				filepath.Join(tmpDir, "context.json"), "test-model",
			)
			Expect(err).NotTo(HaveOccurred())

			eng := engine.New(engine.Config{
				ChatProvider:         &pipelineProvider{},
				Manifest:             manifest,
				TokenCounter:         &pipelineTokenCounter{},
				Store:                contextStore,
				RecallBroker:         broker,
				ContextAssemblyHooks: []plugin.ContextAssemblyHook{realtimeHook},
			})

			msgs := eng.BuildContextWindowForTest(
				stdctx.Background(), "ses-1", "Tell me about observer pattern",
			)

			contents := collectContents(msgs)
			Expect(contents).To(ContainElement(ContainSubstring("observer pattern")))
			Expect(contents).To(ContainElement(ContainSubstring("event-driven architecture")))

			Expect(msgs[len(msgs)-1].Role).To(Equal("user"))
			Expect(msgs[len(msgs)-1].Content).To(Equal("Tell me about observer pattern"))
		})
	})

	Describe("Agent scoping through the pipeline", func() {
		It("filters recalled observations by agent ID", func() {
			agentAEntry := learning.Entry{
				Timestamp: time.Now().Add(-10 * time.Minute), AgentID: "agent-a",
				UserMessage: "What is concurrency?",
				Response:    "Concurrency is executing tasks simultaneously",
				ToolsUsed:   []string{"editor"}, Outcome: "success",
			}
			agentBEntry := learning.Entry{
				Timestamp: time.Now().Add(-5 * time.Minute), AgentID: "agent-b",
				UserMessage: "What is concurrency in Rust?",
				Response:    "Rust uses ownership for safe concurrency",
				ToolsUsed:   []string{"editor"}, Outcome: "success",
			}
			Expect(store.Capture(agentAEntry)).To(Succeed())
			Expect(store.Capture(agentBEntry)).To(Succeed())
			_, _, err := distiller.Distill(agentAEntry)
			Expect(err).NotTo(HaveOccurred())
			_, _, err = distiller.Distill(agentBEntry)
			Expect(err).NotTo(HaveOccurred())

			sourceA := &knowledgeGraphRecallSource{graph: graph, agentID: "agent-a"}
			broker := recall.NewRecallBroker(nil, nil, nil, sourceA)

			contextStore, err := recall.NewFileContextStore(
				filepath.Join(tmpDir, "context.json"), "test-model",
			)
			Expect(err).NotTo(HaveOccurred())

			scopedManifest := manifest
			scopedManifest.ID = "agent-a"

			eng := engine.New(engine.Config{
				ChatProvider: &pipelineProvider{},
				Manifest:     scopedManifest,
				TokenCounter: &pipelineTokenCounter{},
				Store:        contextStore,
				RecallBroker: broker,
			})

			ctx := stdctx.WithValue(stdctx.Background(), learning.AgentIDKey, "agent-a")
			msgs := eng.BuildContextWindowForTest(ctx, "ses-1", "Tell me about concurrency")

			contents := collectContents(msgs)
			Expect(contents).To(ContainElement(ContainSubstring("simultaneously")))

			for _, c := range contents {
				Expect(c).NotTo(ContainSubstring("Rust"))
			}
		})
	})

	Describe("Budget pressure with recalled observations", func() {
		It("accounts for user message tokens in budget when recall returns results", func() {
			entry := learning.Entry{
				Timestamp: time.Now().Add(-5 * time.Minute), AgentID: "test-agent",
				UserMessage: "Short question",
				Response:    "Short answer about testing",
				ToolsUsed:   []string{"editor"}, Outcome: "success",
			}
			Expect(store.Capture(entry)).To(Succeed())
			_, _, err := distiller.Distill(entry)
			Expect(err).NotTo(HaveOccurred())

			tightCounter := &pipelineTokenCounter{
				countFn:      func(text string) int { return len(text) },
				modelLimitFn: func(_ string) int { return 500 },
			}

			recallSource := &knowledgeGraphRecallSource{graph: graph, agentID: "test-agent"}
			broker := recall.NewRecallBroker(nil, nil, nil, recallSource)
			contextStore, err := recall.NewFileContextStore(
				filepath.Join(tmpDir, "context.json"), "test-model",
			)
			Expect(err).NotTo(HaveOccurred())

			eng := engine.New(engine.Config{
				ChatProvider: &pipelineProvider{},
				Manifest:     manifest,
				TokenCounter: tightCounter,
				Store:        contextStore,
				RecallBroker: broker,
			})

			userMsg := "Tell me about testing"
			eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", userMsg)
			result := eng.LastContextResult()
			Expect(result.TokensUsed).To(BeNumerically(">=", len(userMsg)))
			Expect(result.TokensUsed).To(BeNumerically(">", 0))
		})
	})

	Describe("Distiller partial failure", func() {
		It("preserves entities when CreateRelations fails", func() {
			failGraph := &inMemoryKnowledgeGraph{
				createRelationsErr: errors.New("relation storage unavailable"),
			}
			failDistiller := learning.NewStructuredDistiller(failGraph)

			entry := learning.Entry{
				Timestamp: time.Now(), AgentID: "test-agent",
				UserMessage: "What is testing?",
				Response:    "Testing verifies correctness",
				ToolsUsed:   []string{"editor"}, Outcome: "success",
			}

			_, _, err := failDistiller.Distill(entry)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("creating relations"))

			Expect(failGraph.entities).To(HaveLen(1))
			Expect(failGraph.relations).To(BeEmpty())
		})
	})

	Describe("Multi-hook failure composition", func() {
		It("later hooks still contribute after an earlier hook fails", func() {
			failingHook := func(_ stdctx.Context, _ *plugin.ContextAssemblyPayload) error {
				return errors.New("hook-1 failed")
			}

			succeedingHook := func(_ stdctx.Context, payload *plugin.ContextAssemblyPayload) error {
				payload.SearchResults = append(payload.SearchResults, recall.SearchResult{
					MessageID: "hook-2-result",
					Score:     1.0,
					Message: provider.Message{
						Role:    "assistant",
						Content: "Hook-2 contributed this observation",
					},
				})
				return nil
			}

			contextStore, err := recall.NewFileContextStore(
				filepath.Join(tmpDir, "context.json"), "test-model",
			)
			Expect(err).NotTo(HaveOccurred())

			eng := engine.New(engine.Config{
				ChatProvider:         &pipelineProvider{},
				Manifest:             manifest,
				TokenCounter:         &pipelineTokenCounter{},
				Store:                contextStore,
				ContextAssemblyHooks: []plugin.ContextAssemblyHook{failingHook, succeedingHook},
			})

			msgs := eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", "Query")
			contents := collectContents(msgs)
			Expect(contents).To(ContainElement("Hook-2 contributed this observation"))

			Expect(msgs[len(msgs)-1].Role).To(Equal("user"))
			Expect(msgs[len(msgs)-1].Content).To(Equal("Query"))
		})
	})
})

func collectContents(msgs []provider.Message) []string {
	contents := make([]string, len(msgs))
	for i, m := range msgs {
		contents[i] = m.Content
	}
	return contents
}
