package memory_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/memory"
)

var _ = Describe("Types", func() {
	Describe("Entity", func() {
		It("round-trips correctly via JSON", func() {
			entity := memory.Entity{
				Name:         "testEntity",
				EntityType:   "Person",
				Observations: []string{"works at ACME", "knows Go"},
			}
			data, err := json.Marshal(entity)
			Expect(err).NotTo(HaveOccurred())
			var roundTripped memory.Entity
			Expect(json.Unmarshal(data, &roundTripped)).To(Succeed())
			Expect(roundTripped).To(Equal(entity))
		})

		It("uses correct JSON field names", func() {
			entity := memory.Entity{Name: "test", EntityType: "Company", Observations: []string{"big"}}
			data, err := json.Marshal(entity)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(ContainSubstring(`"entityType"`))
			Expect(string(data)).To(ContainSubstring(`"observations"`))
		})
	})

	Describe("Relation", func() {
		It("round-trips correctly via JSON", func() {
			relation := memory.Relation{
				From:         "Alice",
				To:           "Bob",
				RelationType: "knows",
			}
			data, err := json.Marshal(relation)
			Expect(err).NotTo(HaveOccurred())
			var roundTripped memory.Relation
			Expect(json.Unmarshal(data, &roundTripped)).To(Succeed())
			Expect(roundTripped).To(Equal(relation))
		})

		It("uses correct JSON field names", func() {
			relation := memory.Relation{From: "A", To: "B", RelationType: "rel"}
			data, err := json.Marshal(relation)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(ContainSubstring(`"relationType"`))
		})
	})

	Describe("KnowledgeGraph", func() {
		It("round-trips correctly via JSON", func() {
			graph := memory.KnowledgeGraph{
				Entities: []memory.Entity{
					{Name: "Alice", EntityType: "Person", Observations: []string{"tall"}},
				},
				Relations: []memory.Relation{
					{From: "Alice", To: "Bob", RelationType: "knows"},
				},
			}
			data, err := json.Marshal(graph)
			Expect(err).NotTo(HaveOccurred())
			var roundTripped memory.KnowledgeGraph
			Expect(json.Unmarshal(data, &roundTripped)).To(Succeed())
			Expect(roundTripped).To(Equal(graph))
		})
	})
})
