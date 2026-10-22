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

				input := tool.Input{
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

				input := tool.Input{
					Name:      "web",
					Arguments: map[string]interface{}{"url": server.URL},
				}
				result, err := webTool.Execute(context.Background(), input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).ToNot(HaveOccurred())
				Expect(result.Output).To(HaveLen(10 * 1024))
			})
		})

		Context("with HTML responses", func() {
			It("extracts visible text and strips script and style content", func() {
				page := `<!DOCTYPE html>
<html>
<head>
<title>My Page Title</title>
<style>body { color: red; }</style>
</head>
<body>
<script>var secret = "leaked-token";</script>
<p>Hello visible world, this is the main paragraph with plenty of text content.</p>
</body>
</html>`
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(page))
				}))
				testClient = server.Client()
				webTool = web.NewWithClient(testClient)

				input := tool.Input{
					Name:      "web",
					Arguments: map[string]interface{}{"url": server.URL},
				}
				result, err := webTool.Execute(context.Background(), input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).ToNot(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("Hello visible world, this is the main paragraph"))
				Expect(result.Output).To(ContainSubstring("My Page Title"))
				Expect(result.Output).NotTo(ContainSubstring("secret"))
				Expect(result.Output).NotTo(ContainSubstring("leaked-token"))
				Expect(result.Output).NotTo(ContainSubstring("color: red"))
				Expect(result.Output).NotTo(ContainSubstring("<script>"))
				Expect(result.Output).NotTo(ContainSubstring("<style>"))
			})

			It("preserves the title text from the head section", func() {
				page := `<html><head><title>Unique Title Value</title></head><body><p>Body text here that is long enough to exceed the extraction threshold for testing.</p></body></html>`
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/html")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(page))
				}))
				testClient = server.Client()
				webTool = web.NewWithClient(testClient)

				input := tool.Input{
					Name:      "web",
					Arguments: map[string]interface{}{"url": server.URL},
				}
				result, err := webTool.Execute(context.Background(), input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("Unique Title Value"))
			})

			It("falls back to raw body when extraction yields too little text", func() {
				page := `<html><head><style>hidden-rule</style></head><body><script>only-script-content</script></body></html>`
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/html")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(page))
				}))
				testClient = server.Client()
				webTool = web.NewWithClient(testClient)

				input := tool.Input{
					Name:      "web",
					Arguments: map[string]interface{}{"url": server.URL},
				}
				result, err := webTool.Execute(context.Background(), input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("<html>"))
				Expect(result.Output).To(ContainSubstring("<script>"))
			})

			It("caps extracted text at maxBodySize", func() {
				visible := strings.Repeat("a", 20*1024)
				page := "<html><body><p>" + visible + "</p></body></html>"
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/html")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(page))
				}))
				testClient = server.Client()
				webTool = web.NewWithClient(testClient)

				input := tool.Input{
					Name:      "web",
					Arguments: map[string]interface{}{"url": server.URL},
				}
				result, err := webTool.Execute(context.Background(), input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).ToNot(HaveOccurred())
				Expect(result.Output).NotTo(ContainSubstring("<p>"))
				Expect(len(result.Output)).To(BeNumerically("<=", 10*1024))
			})
		})

		Context("with non-HTML responses", func() {
			It("returns plain text unchanged", func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/plain; charset=utf-8")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte("Just plain text, nothing fancy."))
				}))
				testClient = server.Client()
				webTool = web.NewWithClient(testClient)

				input := tool.Input{
					Name:      "web",
					Arguments: map[string]interface{}{"url": server.URL},
				}
				result, err := webTool.Execute(context.Background(), input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(Equal("Just plain text, nothing fancy."))
			})

			It("returns JSON unchanged", func() {
				payload := `{"key":"value","nested":{"n":1}}`
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(payload))
				}))
				testClient = server.Client()
				webTool = web.NewWithClient(testClient)

				input := tool.Input{
					Name:      "web",
					Arguments: map[string]interface{}{"url": server.URL},
				}
				result, err := webTool.Execute(context.Background(), input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(Equal(payload))
			})
		})

		Context("with missing url argument", func() {
			It("returns a Go error", func() {
				input := tool.Input{
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
				input := tool.Input{
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

				input := tool.Input{
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
