package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/web"
)

var _ = Describe("Web Tool", func() {
	var webTool *web.Tool

	BeforeEach(func() {
		webTool = web.New()
	})

	Describe("Name", func() {
		It("returns web", func() {
			Expect(webTool.Name()).To(Equal("web"))
		})
	})

	Describe("Description", func() {
		It("returns a non-empty description", func() {
			Expect(webTool.Description()).NotTo(BeEmpty())
		})
	})

	Describe("Schema", func() {
		It("has url in Required", func() {
			schema := webTool.Schema()
			Expect(schema.Required).To(ContainElement("url"))
		})

		It("defines url property", func() {
			schema := webTool.Schema()
			Expect(schema.Properties).To(HaveKey("url"))
			Expect(schema.Properties["url"].Type).To(Equal("string"))
		})
	})

	Describe("Execute", func() {
		var server *httptest.Server
		var testClient *http.Client

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		Context("with valid URL", func() {
			It("fetches content from a local httptest server", func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte("Hello, World!"))
				}))
				testClient = server.Client()
				webTool = web.NewWithClient(testClient)

				input := tool.ToolInput{
					Name:      "web",
					Arguments: map[string]interface{}{"url": server.URL},
				}
				result, err := webTool.Execute(context.Background(), input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).ToNot(HaveOccurred())
				Expect(result.Output).To(Equal("Hello, World!"))
			})
		})

		Context("with large response", func() {
			It("truncates response body to 10KB", func() {
				largeBody := strings.Repeat("x", 20*1024)
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(largeBody))
				}))
				testClient = server.Client()
				webTool = web.NewWithClient(testClient)

				input := tool.ToolInput{
					Name:      "web",
					Arguments: map[string]interface{}{"url": server.URL},
				}
				result, err := webTool.Execute(context.Background(), input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).ToNot(HaveOccurred())
				Expect(result.Output).To(HaveLen(10 * 1024))
			})
		})

		Context("with missing url argument", func() {
			It("returns a Go error", func() {
				input := tool.ToolInput{
					Name:      "web",
					Arguments: map[string]interface{}{},
				}
				_, err := webTool.Execute(context.Background(), input)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("url"))
			})
		})

		Context("with invalid URL", func() {
			It("returns non-nil Error in result", func() {
				input := tool.ToolInput{
					Name:      "web",
					Arguments: map[string]interface{}{"url": "://invalid"},
				}
				result, err := webTool.Execute(context.Background(), input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).To(HaveOccurred())
				Expect(result.Error.Error()).To(ContainSubstring("invalid"))
			})
		})

		Context("with context cancellation", func() {
			It("respects context cancellation", func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					time.Sleep(5 * time.Second)
					w.WriteHeader(http.StatusOK)
				}))
				testClient = server.Client()
				webTool = web.NewWithClient(testClient)

				ctx, cancel := context.WithCancel(context.Background())
				cancel()

				input := tool.ToolInput{
					Name:      "web",
					Arguments: map[string]interface{}{"url": server.URL},
				}
				result, err := webTool.Execute(ctx, input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).To(HaveOccurred())
			})
		})
	})
})
