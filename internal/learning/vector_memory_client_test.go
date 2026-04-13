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
})
