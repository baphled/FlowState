package memory

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Graph", func() {
	var (
		graph *Graph
	)

	BeforeEach(func() {
		graph = NewGraph()
	})

	Describe("CreateEntities", func() {
		It("adds new entities and skips duplicates", func() {
			entities := []Entity{
				{Name: "A", EntityType: "Type1", Observations: []string{"foo"}},
				{Name: "B", EntityType: "Type2", Observations: []string{"bar"}},
			}
			added := graph.CreateEntities(entities)
			Expect(added).To(HaveLen(2))
			Expect(graph.entities).To(ContainElements(entities[0], entities[1]))

			// Try adding duplicate
			added2 := graph.CreateEntities([]Entity{{Name: "A", EntityType: "Type1", Observations: []string{"baz"}}})
			Expect(added2).To(BeEmpty())
			Expect(graph.entities).To(HaveLen(2))
		})
	})

	Describe("CreateRelations", func() {
		It("adds new relations and skips duplicates", func() {
			graph.CreateEntities([]Entity{{Name: "A", EntityType: "T", Observations: nil}, {Name: "B", EntityType: "T", Observations: nil}})
			rels := []Relation{{From: "A", To: "B", RelationType: "knows"}}
			added := graph.CreateRelations(rels)
			Expect(added).To(HaveLen(1))
			Expect(graph.relations).To(ContainElement(rels[0]))

			// Try adding duplicate
			added2 := graph.CreateRelations([]Relation{{From: "A", To: "B", RelationType: "knows"}})
			Expect(added2).To(BeEmpty())
			Expect(graph.relations).To(HaveLen(1))
		})
	})

	Describe("AddObservations", func() {
		It("adds observations to an existing entity", func() {
			graph.CreateEntities([]Entity{{Name: "A", EntityType: "T", Observations: []string{"foo"}}})
			err := graph.AddObservations("A", []string{"bar", "baz"})
			Expect(err).NotTo(HaveOccurred())
			Expect(graph.entities[0].Observations).To(ContainElements("foo", "bar", "baz"))
		})
		It("returns error if entity not found", func() {
			err := graph.AddObservations("Z", []string{"nope"})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("DeleteEntities", func() {
		It("removes entities and cascades relations", func() {
			graph.CreateEntities([]Entity{{Name: "A", EntityType: "T", Observations: nil}, {Name: "B", EntityType: "T", Observations: nil}})
			graph.CreateRelations([]Relation{{From: "A", To: "B", RelationType: "knows"}, {From: "B", To: "A", RelationType: "knows"}})
			graph.DeleteEntities([]string{"A"})
			Expect(graph.entities).To(HaveLen(1))
			Expect(graph.entities[0].Name).To(Equal("B"))
			// All relations with A as From or To should be gone
			Expect(graph.relations).To(BeEmpty())
		})
	})

	Describe("DeleteObservations", func() {
		It("removes specific observations from entity", func() {
			graph.CreateEntities([]Entity{{Name: "A", EntityType: "T", Observations: []string{"foo", "bar", "baz"}}})
			err := graph.DeleteObservations("A", []string{"bar"})
			Expect(err).NotTo(HaveOccurred())
			Expect(graph.entities[0].Observations).To(ConsistOf("foo", "baz"))
		})
		It("returns error if entity not found", func() {
			err := graph.DeleteObservations("Z", []string{"nope"})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("DeleteRelations", func() {
		It("removes specific relations", func() {
			graph.CreateEntities([]Entity{{Name: "A", EntityType: "T", Observations: nil}, {Name: "B", EntityType: "T", Observations: nil}})
			rels := []Relation{{From: "A", To: "B", RelationType: "knows"}, {From: "B", To: "A", RelationType: "knows"}}
			graph.CreateRelations(rels)
			graph.DeleteRelations([]Relation{{From: "A", To: "B", RelationType: "knows"}})
			Expect(graph.relations).To(ConsistOf(rels[1]))
		})
	})

	Describe("ReadGraph", func() {
		It("returns the full graph", func() {
			ents := []Entity{{Name: "A", EntityType: "T", Observations: nil}, {Name: "B", EntityType: "T", Observations: nil}}
			rels := []Relation{{From: "A", To: "B", RelationType: "knows"}}
			graph.CreateEntities(ents)
			graph.CreateRelations(rels)
			kg := graph.ReadGraph()
			Expect(kg.Entities).To(ConsistOf(ents[0], ents[1]))
			Expect(kg.Relations).To(ConsistOf(rels[0]))
		})
	})

	Describe("SearchNodes", func() {
		It("finds entities by case-insensitive substring in name, type, or observations", func() {
			graph.CreateEntities([]Entity{
				{Name: "Alpha", EntityType: "TypeA", Observations: []string{"foo bar"}},
				{Name: "Beta", EntityType: "TypeB", Observations: []string{"baz"}},
				{Name: "Gamma", EntityType: "TypeC", Observations: []string{"protocol"}},
			})
			results := graph.SearchNodes("alpha")
			Expect(results).To(HaveLen(1))
			Expect(results[0].Name).To(Equal("Alpha"))

			results = graph.SearchNodes("typeb")
			Expect(results).To(HaveLen(1))
			Expect(results[0].Name).To(Equal("Beta"))

			results = graph.SearchNodes("proto")
			Expect(results).To(HaveLen(1))
			Expect(results[0].Name).To(Equal("Gamma"))
		})
	})

	Describe("OpenNodes", func() {
		It("returns only requested entities and relations between them", func() {
			graph.CreateEntities([]Entity{
				{Name: "A", EntityType: "T", Observations: nil},
				{Name: "B", EntityType: "T", Observations: nil},
				{Name: "C", EntityType: "T", Observations: nil},
			})
			rels := []Relation{
				{From: "A", To: "B", RelationType: "knows"},
				{From: "B", To: "C", RelationType: "knows"},
				{From: "A", To: "C", RelationType: "knows"},
			}
			graph.CreateRelations(rels)
			ents, relsOut := graph.OpenNodes([]string{"A", "C"})
			Expect(ents).To(HaveLen(2))
			Expect(ents).To(ContainElements(
				Entity{Name: "A", EntityType: "T", Observations: nil},
				Entity{Name: "C", EntityType: "T", Observations: nil},
			))
			Expect(relsOut).To(HaveLen(1))
			Expect(relsOut[0]).To(Equal(Relation{From: "A", To: "C", RelationType: "knows"}))
		})
	})
})
