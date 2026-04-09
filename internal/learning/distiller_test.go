package learning_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/learning"
)

type mockMemoryClient struct {
	createEntitiesCalled   bool
	createEntitiesEntities []learning.Entity
	createRelationsCalled  bool
	createRelationsRels    []learning.Relation
}

func (m *mockMemoryClient) CreateEntities(ctx context.Context, entities []learning.Entity) ([]learning.Entity, error) {
	m.createEntitiesCalled = true
	m.createEntitiesEntities = entities
	return entities, nil
}

func (m *mockMemoryClient) CreateRelations(ctx context.Context, relations []learning.Relation) ([]learning.Relation, error) {
	m.createRelationsCalled = true
	m.createRelationsRels = relations
	return relations, nil
}

// Other methods not used in Distill, so panic or return nil.
func (m *mockMemoryClient) AddObservations(ctx context.Context, observations []learning.ObservationEntry) ([]learning.ObservationEntry, error) {
	panic("not implemented")
}
func (m *mockMemoryClient) DeleteEntities(ctx context.Context, entityNames []string) ([]string, error) {
	panic("not implemented")
}
func (m *mockMemoryClient) DeleteObservations(ctx context.Context, deletions []learning.DeletionEntry) error {
	panic("not implemented")
}
func (m *mockMemoryClient) DeleteRelations(ctx context.Context, relations []learning.Relation) error {
	panic("not implemented")
}
func (m *mockMemoryClient) ReadGraph(ctx context.Context) (learning.KnowledgeGraph, error) {
	panic("not implemented")
}
func (m *mockMemoryClient) SearchNodes(ctx context.Context, query string) ([]learning.Entity, error) {
	panic("not implemented")
}
func (m *mockMemoryClient) OpenNodes(ctx context.Context, names []string) (learning.KnowledgeGraph, error) {
	panic("not implemented")
}
func (m *mockMemoryClient) WriteLearningRecord(record *learning.Record) error {
	panic("not implemented")
}

var _ = Describe("StructuredDistiller", func() {
	var (
		distiller learning.Distiller
		client    *mockMemoryClient
	)

	BeforeEach(func() {
		client = &mockMemoryClient{}
		distiller = learning.NewStructuredDistiller(client)
	})

	Describe("Distill", func() {
		It("extracts fields from Entry and creates Entity and Relations", func() {
			entry := learning.Entry{
				Timestamp:   time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
				AgentID:     "agent-123",
				UserMessage: "How to test?",
				Response:    "Use Ginkgo",
				ToolsUsed:   []string{"editor", "terminal"},
				Outcome:     "success",
			}

			entity, relations, err := distiller.Distill(entry)
			Expect(err).NotTo(HaveOccurred())

			// Check Entity
			Expect(entity.Name).To(Equal("agent-123"))
			Expect(entity.EntityType).To(Equal("observation"))
			Expect(entity.Observations).To(ContainElement("AgentID: agent-123"))
			Expect(entity.Observations).To(ContainElement("UserMessage: How to test?"))
			Expect(entity.Observations).To(ContainElement("Response: Use Ginkgo"))
			Expect(entity.Observations).To(ContainElement("ToolsUsed: [editor terminal]"))
			Expect(entity.Observations).To(ContainElement("Outcome: success"))
			Expect(entity.Observations).To(ContainElement("Timestamp: 2023-01-01T12:00:00Z"))

			// Check Relations
			Expect(relations).To(HaveLen(2))
			toolNames := make([]string, len(relations))
			for i, rel := range relations {
				Expect(rel.From).To(Equal("agent-123"))
				Expect(rel.RelationType).To(Equal("used_tool"))
				toolNames[i] = rel.To
			}
			Expect(toolNames).To(ContainElement("editor"))
			Expect(toolNames).To(ContainElement("terminal"))

			// Check MemoryClient calls
			Expect(client.createEntitiesCalled).To(BeTrue())
			Expect(client.createEntitiesEntities).To(HaveLen(1))
			Expect(client.createEntitiesEntities[0]).To(Equal(entity))

			Expect(client.createRelationsCalled).To(BeTrue())
			Expect(client.createRelationsRels).To(Equal(relations))
		})

		It("handles empty ToolsUsed", func() {
			entry := learning.Entry{
				Timestamp:   time.Now(),
				AgentID:     "agent-456",
				UserMessage: "Simple question",
				Response:    "Simple answer",
				ToolsUsed:   []string{},
				Outcome:     "success",
			}

			entity, relations, err := distiller.Distill(entry)
			Expect(err).NotTo(HaveOccurred())

			Expect(entity.Name).To(Equal("agent-456"))
			Expect(relations).To(BeEmpty())

			Expect(client.createEntitiesCalled).To(BeTrue())
			Expect(client.createRelationsCalled).To(BeTrue()) // Even if empty
		})
	})
})
