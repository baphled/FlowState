package websearch_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/websearch"
)

var _ = Describe("Websearch Tool", func() {
	var searchTool *websearch.Tool

	BeforeEach(func() {
		searchTool = websearch.New("https://api.exa.ai/search", "test-key")
	})

	Describe("Name", func() {
		It("returns websearch", func() {
			Expect(searchTool.Name()).To(Equal("websearch"))
		})
	})

	Describe("Description", func() {
		It("describes Exa web search", func() {
			Expect(searchTool.Description()).To(ContainSubstring("Exa"))
		})
	})

	Describe("Schema", func() {
		It("requires a query", func() {
			schema := searchTool.Schema()
			Expect(schema.Required).To(ContainElement("query"))
		})

		It("exposes configurable result count", func() {
			schema := searchTool.Schema()
			Expect(schema.Properties).To(HaveKey("numResults"))
			Expect(schema.Properties["numResults"].Type).To(Equal("integer"))
		})

		It("exposes configurable timeout", func() {
			schema := searchTool.Schema()
			Expect(schema.Properties).To(HaveKey("timeout"))
			Expect(schema.Properties["timeout"].Type).To(Equal("integer"))
		})
	})

	Describe("Execute", func() {
		var server *httptest.Server

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		It("posts the search query and returns results", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Method).To(Equal(http.MethodPost))
				Expect(r.Header.Get("x-api-key")).To(Equal("test-key"))
				Expect(r.Header.Get("content-type")).To(ContainSubstring("application/json"))
				_, _ = w.Write([]byte(`{"results":[{"title":"Result 1","url":"https://example.com","text":"Snippet"}]}`))
			}))
			searchTool = websearch.New(server.URL, "test-key")

			result, err := searchTool.Execute(context.Background(), tool.Input{
				Name: "websearch",
				Arguments: map[string]interface{}{
					"query":      "latest ai news",
					"numResults": 3,
				},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("Result 1"))
			Expect(result.Output).To(ContainSubstring("https://example.com"))
		})

		It("returns a tool error when the query is missing", func() {
			result, err := searchTool.Execute(context.Background(), tool.Input{Name: "websearch", Arguments: map[string]interface{}{}})

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).To(HaveOccurred())
		})

		It("respects context cancellation", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(2 * time.Second)
			}))
			searchTool = websearch.New(server.URL, "test-key")

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			result, err := searchTool.Execute(ctx, tool.Input{Name: "websearch", Arguments: map[string]interface{}{"query": "hello"}})

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).To(HaveOccurred())
		})

		It("surfaces API failures in the tool result", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"boom"}`))
			}))
			searchTool = websearch.New(server.URL, "test-key")

			result, err := searchTool.Execute(context.Background(), tool.Input{Name: "websearch", Arguments: map[string]interface{}{"query": "hello"}})

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).To(HaveOccurred())
			Expect(result.Error.Error()).To(ContainSubstring("search failed"))
		})

		It("applies the requested timeout", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(200 * time.Millisecond)
				_, _ = w.Write([]byte(`{"results":[]}`))
			}))
			searchTool = websearch.New(server.URL, "test-key")

			result, err := searchTool.Execute(context.Background(), tool.Input{Name: "websearch", Arguments: map[string]interface{}{"query": "hello", "timeout": 1}})

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).To(HaveOccurred())
		})
	})
})
