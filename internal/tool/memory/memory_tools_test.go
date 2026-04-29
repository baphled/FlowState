package memory_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/learning"
	toolmemory "github.com/baphled/flowstate/internal/tool/memory"
	"github.com/baphled/flowstate/internal/tool"
)

// stubMemoryClient satisfies learning.MemoryClient for tests.
type stubMemoryClient struct {
	searchResult []learning.Entity
	searchErr    error
	openResult   learning.KnowledgeGraph
	openErr      error
}

func (s *stubMemoryClient) SearchNodes(_ context.Context, _ string) ([]learning.Entity, error) {
	return s.searchResult, s.searchErr
}

func (s *stubMemoryClient) OpenNodes(_ context.Context, _ []string) (learning.KnowledgeGraph, error) {
	return s.openResult, s.openErr
}

func (s *stubMemoryClient) CreateEntities(_ context.Context, e []learning.Entity) ([]learning.Entity, error) {
	return e, nil
}
func (s *stubMemoryClient) CreateRelations(_ context.Context, r []learning.Relation) ([]learning.Relation, error) {
	return r, nil
}
func (s *stubMemoryClient) AddObservations(_ context.Context, o []learning.ObservationEntry) ([]learning.ObservationEntry, error) {
	return o, nil
}
func (s *stubMemoryClient) DeleteEntities(_ context.Context, names []string) ([]string, error) {
	return names, nil
}
func (s *stubMemoryClient) DeleteObservations(_ context.Context, _ []learning.DeletionEntry) error {
	return nil
}
func (s *stubMemoryClient) DeleteRelations(_ context.Context, _ []learning.Relation) error {
	return nil
}
func (s *stubMemoryClient) ReadGraph(_ context.Context) (learning.KnowledgeGraph, error) {
	return learning.KnowledgeGraph{}, nil
}
func (s *stubMemoryClient) WriteLearningRecord(_ *learning.Record) error { return nil }

var _ = Describe("mcp_memory_search_nodes", func() {
	It("returns formatted entity list for a matching query", func() {
		client := &stubMemoryClient{
			searchResult: []learning.Entity{
				{Name: "golang", EntityType: "language", Observations: []string{"statically typed", "compiled"}},
				{Name: "channels", EntityType: "concept", Observations: []string{"goroutine communication"}},
			},
		}
		t := toolmemory.NewSearchNodesTool(client)

		result, err := t.Execute(context.Background(), tool.Input{
			Name:      "mcp_memory_search_nodes",
			Arguments: map[string]interface{}{"query": "go concurrency"},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Output).To(ContainSubstring("golang"))
		Expect(result.Output).To(ContainSubstring("statically typed"))
		Expect(result.Output).To(ContainSubstring("channels"))
	})

	It("returns a no-results message when search returns empty", func() {
		client := &stubMemoryClient{searchResult: []learning.Entity{}}
		t := toolmemory.NewSearchNodesTool(client)

		result, err := t.Execute(context.Background(), tool.Input{
			Name:      "mcp_memory_search_nodes",
			Arguments: map[string]interface{}{"query": "nothing"},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Output).To(ContainSubstring("no results"))
	})

	It("returns an error when query argument is missing", func() {
		t := toolmemory.NewSearchNodesTool(&stubMemoryClient{})

		_, err := t.Execute(context.Background(), tool.Input{
			Name:      "mcp_memory_search_nodes",
			Arguments: map[string]interface{}{},
		})

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("query"))
	})

	It("propagates search errors", func() {
		client := &stubMemoryClient{searchErr: errors.New("qdrant unavailable")}
		t := toolmemory.NewSearchNodesTool(client)

		_, err := t.Execute(context.Background(), tool.Input{
			Name:      "mcp_memory_search_nodes",
			Arguments: map[string]interface{}{"query": "test"},
		})

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("qdrant unavailable"))
	})

	It("has the correct tool name and requires query in schema", func() {
		t := toolmemory.NewSearchNodesTool(&stubMemoryClient{})
		Expect(t.Name()).To(Equal("mcp_memory_search_nodes"))
		Expect(t.Schema().Required).To(ContainElement("query"))
	})
})

var _ = Describe("mcp_memory_open_nodes", func() {
	It("returns formatted graph for known node names", func() {
		client := &stubMemoryClient{
			openResult: learning.KnowledgeGraph{
				Entities: []learning.Entity{
					{Name: "goroutine", EntityType: "concept", Observations: []string{"lightweight thread"}},
				},
				Relations: []learning.Relation{
					{From: "goroutine", RelationType: "uses", To: "channel"},
				},
			},
		}
		t := toolmemory.NewOpenNodesTool(client)

		result, err := t.Execute(context.Background(), tool.Input{
			Name:      "mcp_memory_open_nodes",
			Arguments: map[string]interface{}{"names": []interface{}{"goroutine"}},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Output).To(ContainSubstring("goroutine"))
		Expect(result.Output).To(ContainSubstring("lightweight thread"))
		Expect(result.Output).To(ContainSubstring("channel"))
	})

	It("returns a no-results message when graph is empty", func() {
		client := &stubMemoryClient{openResult: learning.KnowledgeGraph{}}
		t := toolmemory.NewOpenNodesTool(client)

		result, err := t.Execute(context.Background(), tool.Input{
			Name:      "mcp_memory_open_nodes",
			Arguments: map[string]interface{}{"names": []interface{}{"unknown"}},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Output).To(ContainSubstring("no nodes"))
	})

	It("returns an error when names argument is missing", func() {
		t := toolmemory.NewOpenNodesTool(&stubMemoryClient{})

		_, err := t.Execute(context.Background(), tool.Input{
			Name:      "mcp_memory_open_nodes",
			Arguments: map[string]interface{}{},
		})

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("names"))
	})

	It("propagates open errors", func() {
		client := &stubMemoryClient{openErr: errors.New("store offline")}
		t := toolmemory.NewOpenNodesTool(client)

		_, err := t.Execute(context.Background(), tool.Input{
			Name:      "mcp_memory_open_nodes",
			Arguments: map[string]interface{}{"names": []interface{}{"anything"}},
		})

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("store offline"))
	})

	It("has the correct tool name and requires names in schema", func() {
		t := toolmemory.NewOpenNodesTool(&stubMemoryClient{})
		Expect(t.Name()).To(Equal("mcp_memory_open_nodes"))
		Expect(t.Schema().Required).To(ContainElement("names"))
	})
})
