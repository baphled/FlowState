package learning_test

import (
	"context"

	"github.com/baphled/flowstate/internal/learning"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The mockVectorStore and mockEmbedder types are shared with mem0_store_test.go.

var _ = Describe("VectorStoreMemoryClient Qdrant point ID contract", func() {
	var (
		vs     *mockVectorStore
		emb    *mockEmbedder
		client *learning.VectorStoreMemoryClient
	)

	BeforeEach(func() {
		vs = &mockVectorStore{}
		emb = &mockEmbedder{vector: []float64{0.1, 0.2, 0.3}}
		client = learning.NewVectorStoreMemoryClient(vs, emb, "flowstate-col")
	})

	Describe("CreateEntities (distiller path)", func() {
		It("upserts a UUID point ID, not the raw session- source string", func() {
			_, err := client.CreateEntities(context.Background(), []learning.Entity{
				{Name: "session-1776075781962028658", EntityType: "observation"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(vs.upsertPoints).To(HaveLen(1))
			Expect(vs.upsertPoints[0].ID).To(HaveLen(36))
			Expect(vs.upsertPoints[0].ID).To(MatchRegexp(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`))
		})

		It("preserves the original entity Name in the payload as source_id", func() {
			_, err := client.CreateEntities(context.Background(), []learning.Entity{
				{Name: "session-1776075781962028658", EntityType: "observation"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(vs.upsertPoints[0].Payload).To(HaveKeyWithValue("source_id", "session-1776075781962028658"))
		})

		It("is deterministic: re-creating the same entity produces the same point ID", func() {
			_, err := client.CreateEntities(context.Background(), []learning.Entity{
				{Name: "session-X", EntityType: "observation"},
			})
			Expect(err).NotTo(HaveOccurred())
			_, err = client.CreateEntities(context.Background(), []learning.Entity{
				{Name: "session-X", EntityType: "observation"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(vs.upsertPoints).To(HaveLen(2))
			Expect(vs.upsertPoints[0].ID).To(Equal(vs.upsertPoints[1].ID))
		})

		It("shares the same helper as mem0 capture: identical source strings produce identical UUIDs across callers", func() {
			_, err := client.CreateEntities(context.Background(), []learning.Entity{
				{Name: "shared-id-42", EntityType: "observation"},
			})
			Expect(err).NotTo(HaveOccurred())
			fromEntity := vs.upsertPoints[0].ID
			// Helper-derived value from the shared package helper.
			Expect(fromEntity).To(Equal(learning.PointIDFromSource("shared-id-42")))
		})
	})

	Describe("CreateRelations", func() {
		It("upserts a UUID point ID and stores the composed relation string as source_id", func() {
			_, err := client.CreateRelations(context.Background(), []learning.Relation{
				{From: "session-1", RelationType: "used_tool", To: "bash"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(vs.upsertPoints).To(HaveLen(1))
			Expect(vs.upsertPoints[0].ID).To(MatchRegexp(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`))
			Expect(vs.upsertPoints[0].Payload).To(HaveKeyWithValue("source_id", "session-1:used_tool:bash"))
		})
	})

	Describe("WriteLearningRecord", func() {
		It("upserts a UUID point ID and preserves the AgentID as source_id", func() {
			err := client.WriteLearningRecord(&learning.Record{
				AgentID:   "session-1776075781962028658",
				ToolsUsed: []string{"bash"},
				Outcome:   "success",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(vs.upsertPoints).To(HaveLen(1))
			Expect(vs.upsertPoints[0].ID).To(MatchRegexp(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`))
			Expect(vs.upsertPoints[0].Payload).To(HaveKeyWithValue("source_id", "session-1776075781962028658"))
		})
	})

	Describe("AddObservations", func() {
		It("upserts a UUID point ID and preserves the EntityName as source_id", func() {
			_, err := client.AddObservations(context.Background(), []learning.ObservationEntry{
				{EntityName: "session-1", Contents: []string{"obs-a", "obs-b"}},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(vs.upsertPoints).To(HaveLen(1))
			Expect(vs.upsertPoints[0].ID).To(MatchRegexp(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`))
			Expect(vs.upsertPoints[0].Payload).To(HaveKeyWithValue("source_id", "session-1"))
		})
	})

	Describe("SearchNodes", func() {
		It("uses source_id from payload as entity name, not the UUID point ID", func() {
			vs.searchResult = []learning.ScoredVectorPoint{
				{ID: "88ae915d-f474-5c13-b6cd-da740c11d313", Score: 0.9, Payload: map[string]any{
					"source_id":  "golang-concurrency",
					"entityType": "concept",
					"observations": []string{"channels enable goroutine communication"},
				}},
			}
			entities, err := client.SearchNodes(context.Background(), "goroutine channels")
			Expect(err).NotTo(HaveOccurred())
			Expect(entities).To(HaveLen(1))
			Expect(entities[0].Name).To(Equal("golang-concurrency"))
			Expect(entities[0].EntityType).To(Equal("concept"))
			Expect(entities[0].Observations).To(ContainElement("channels enable goroutine communication"))
		})

		It("falls back to UUID point ID when source_id is absent", func() {
			vs.searchResult = []learning.ScoredVectorPoint{
				{ID: "deadbeef-0000-5000-8000-000000000001", Score: 0.8, Payload: map[string]any{}},
			}
			entities, err := client.SearchNodes(context.Background(), "anything")
			Expect(err).NotTo(HaveOccurred())
			Expect(entities[0].Name).To(Equal("deadbeef-0000-5000-8000-000000000001"))
		})

		It("builds observations from learning-record payload shape (content/response/outcome)", func() {
			vs.searchResult = []learning.ScoredVectorPoint{
				{ID: "some-uuid", Score: 0.7, Payload: map[string]any{
					"source_id": "1714389234000000000",
					"agent_id":  "default-assistant",
					"content":   "fix the nil pointer bug",
					"response":  "added nil guard in handler",
					"outcome":   "success",
				}},
			}
			entities, err := client.SearchNodes(context.Background(), "nil pointer bug")
			Expect(err).NotTo(HaveOccurred())
			Expect(entities[0].Name).To(Equal("1714389234000000000"))
			Expect(entities[0].EntityType).To(Equal("default-assistant"))
			obs := entities[0].Observations
			Expect(obs).To(ContainElement(ContainSubstring("fix the nil pointer bug")))
			Expect(obs).To(ContainElement(ContainSubstring("added nil guard in handler")))
			Expect(obs).To(ContainElement(ContainSubstring("success")))
		})
	})

	Describe("OpenNodes", func() {
		It("builds observations from entity-style payload", func() {
			vs.searchResult = []learning.ScoredVectorPoint{
				{ID: "some-uuid", Score: 0.9, Payload: map[string]any{
					"source_id":    "goroutine",
					"entityType":   "concept",
					"observations": []string{"lightweight thread"},
				}},
			}
			graph, err := client.OpenNodes(context.Background(), []string{"goroutine"})
			Expect(err).NotTo(HaveOccurred())
			Expect(graph.Entities).To(HaveLen(1))
			Expect(graph.Entities[0].Observations).To(ContainElement("lightweight thread"))
		})

		It("builds observations from learning-record payload shape", func() {
			vs.searchResult = []learning.ScoredVectorPoint{
				{ID: "some-uuid", Score: 0.9, Payload: map[string]any{
					"source_id": "1714389234000000000",
					"agent_id":  "analyst",
					"content":   "what caused the nil panic",
					"response":  "missing guard on line 42",
					"outcome":   "resolved",
				}},
			}
			graph, err := client.OpenNodes(context.Background(), []string{"nil-panic"})
			Expect(err).NotTo(HaveOccurred())
			Expect(graph.Entities).To(HaveLen(1))
			obs := graph.Entities[0].Observations
			Expect(obs).To(ContainElement(ContainSubstring("what caused the nil panic")))
			Expect(obs).To(ContainElement(ContainSubstring("missing guard on line 42")))
		})
	})
})
