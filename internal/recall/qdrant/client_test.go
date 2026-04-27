package qdrant_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/recall/qdrant"
)

var _ = Describe("QdrantError", func() {
	Describe("Error", func() {
		It("formats with HTTP status and message", func() {
			err := &qdrant.Error{StatusCode: 404, Message: "not found"}
			Expect(err.Error()).To(Equal("qdrant: HTTP 404: not found"))
		})
	})

	Describe("Is", func() {
		It("matches other QdrantError instances", func() {
			err := &qdrant.Error{StatusCode: 500, Message: "internal error"}
			Expect(errors.Is(err, &qdrant.Error{})).To(BeTrue())
		})
	})
})

var _ = Describe("Client", func() {
	var (
		server *httptest.Server
		client *qdrant.Client
	)

	Describe("NewClient", func() {
		It("creates a client with default HTTP client when nil is passed", func() {
			c := qdrant.NewClient("http://localhost:6333", "", nil)
			Expect(c).NotTo(BeNil())
		})
	})

	Describe("CreateCollection", func() {
		Context("when the server returns 200", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.Method).To(Equal(http.MethodPut))
					Expect(r.URL.Path).To(Equal("/collections/test-collection"))

					body, err := io.ReadAll(r.Body)
					Expect(err).NotTo(HaveOccurred())

					var payload map[string]any
					Expect(json.Unmarshal(body, &payload)).To(Succeed())
					Expect(payload).To(HaveKey("vectors"))

					w.WriteHeader(http.StatusOK)
				}))
				DeferCleanup(server.Close)
				client = qdrant.NewClient(server.URL, "", server.Client())
			})

			It("sends PUT to /collections/{name} and succeeds", func() {
				err := client.CreateCollection(context.Background(), "test-collection", qdrant.CollectionConfig{
					VectorSize: 768,
					Distance:   "Cosine",
				})
				Expect(err).NotTo(HaveOccurred())
			})
		})
	})

	Describe("Upsert", func() {
		Context("when the server returns 200 with wait=true", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.Method).To(Equal(http.MethodPut))
					Expect(r.URL.Path).To(Equal("/collections/test-collection/points"))
					Expect(r.URL.Query().Get("wait")).To(Equal("true"))

					body, err := io.ReadAll(r.Body)
					Expect(err).NotTo(HaveOccurred())

					var payload map[string]any
					Expect(json.Unmarshal(body, &payload)).To(Succeed())
					Expect(payload).To(HaveKey("points"))

					w.WriteHeader(http.StatusOK)
				}))
				DeferCleanup(server.Close)
				client = qdrant.NewClient(server.URL, "", server.Client())
			})

			It("sends PUT with wait query parameter and succeeds", func() {
				points := []qdrant.Point{
					{ID: "1", Vector: []float64{0.1, 0.2, 0.3}, Payload: map[string]any{"key": "value"}},
				}
				err := client.Upsert(context.Background(), "test-collection", points, true)
				Expect(err).NotTo(HaveOccurred())
			})
		})
	})

	Describe("Search", func() {
		Context("when the server returns results", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.Method).To(Equal(http.MethodPost))
					Expect(r.URL.Path).To(Equal("/collections/test-collection/points/search"))

					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"result":[{"id":"pt-1","score":0.99,"payload":{"key":"value"}}]}`))
				}))
				DeferCleanup(server.Close)
				client = qdrant.NewClient(server.URL, "", server.Client())
			})

			It("returns parsed ScoredPoints", func() {
				results, err := client.Search(context.Background(), "test-collection", []float64{0.1, 0.2}, 10)
				Expect(err).NotTo(HaveOccurred())
				Expect(results).To(HaveLen(1))
				Expect(results[0].ID).To(Equal("pt-1"))
				Expect(results[0].Score).To(BeNumerically("~", 0.99, 0.001))
				Expect(results[0].Payload).To(HaveKeyWithValue("key", "value"))
			})
		})

		Context("when the server returns integer IDs (legacy mem0/OpenCode writes)", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"result":[{"id":1776075781962028658,"score":0.42,"payload":{"text":"hello"}}]}`))
				}))
				DeferCleanup(server.Close)
				client = qdrant.NewClient(server.URL, "", server.Client())
			})

			It("decodes the integer id into the string ID field without error", func() {
				results, err := client.Search(context.Background(), "opencode_memory", []float64{0.1, 0.2}, 10)
				Expect(err).NotTo(HaveOccurred())
				Expect(results).To(HaveLen(1))
				Expect(results[0].ID).To(Equal("1776075781962028658"))
				Expect(results[0].Payload).To(HaveKeyWithValue("text", "hello"))
			})
		})
	})

	Describe("DeleteCollection", func() {
		Context("when the server returns 200", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.Method).To(Equal(http.MethodDelete))
					Expect(r.URL.Path).To(Equal("/collections/test-collection"))
					w.WriteHeader(http.StatusOK)
				}))
				DeferCleanup(server.Close)
				client = qdrant.NewClient(server.URL, "", server.Client())
			})

			It("sends DELETE and succeeds", func() {
				err := client.DeleteCollection(context.Background(), "test-collection")
				Expect(err).NotTo(HaveOccurred())
			})
		})
	})

	Describe("CollectionExists", func() {
		Context("when the server returns 200", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.Method).To(Equal(http.MethodGet))
					w.WriteHeader(http.StatusOK)
				}))
				DeferCleanup(server.Close)
				client = qdrant.NewClient(server.URL, "", server.Client())
			})

			It("returns true", func() {
				exists, err := client.CollectionExists(context.Background(), "test-collection")
				Expect(err).NotTo(HaveOccurred())
				Expect(exists).To(BeTrue())
			})
		})

		Context("when the server returns 404", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				}))
				DeferCleanup(server.Close)
				client = qdrant.NewClient(server.URL, "", server.Client())
			})

			It("returns false", func() {
				exists, err := client.CollectionExists(context.Background(), "test-collection")
				Expect(err).NotTo(HaveOccurred())
				Expect(exists).To(BeFalse())
			})
		})
	})

	Describe("error handling", func() {
		Context("when the server returns 400", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte("bad request"))
				}))
				DeferCleanup(server.Close)
				client = qdrant.NewClient(server.URL, "", server.Client())
			})

			It("returns a QdrantError with status 400", func() {
				err := client.CreateCollection(context.Background(), "test", qdrant.CollectionConfig{
					VectorSize: 768,
					Distance:   "Cosine",
				})
				Expect(err).To(HaveOccurred())

				var qErr *qdrant.Error
				Expect(errors.As(err, &qErr)).To(BeTrue())
				Expect(qErr.StatusCode).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when the server returns 500", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte("internal error"))
				}))
				DeferCleanup(server.Close)
				client = qdrant.NewClient(server.URL, "", server.Client())
			})

			It("returns a QdrantError with status 500", func() {
				err := client.DeleteCollection(context.Background(), "test")
				Expect(err).To(HaveOccurred())

				var qErr *qdrant.Error
				Expect(errors.As(err, &qErr)).To(BeTrue())
				Expect(qErr.StatusCode).To(Equal(http.StatusInternalServerError))
			})
		})
	})

	Describe("timeout handling", func() {
		It("returns an error without panicking when the server is slow", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(time.Second)
				w.WriteHeader(http.StatusOK)
			}))
			DeferCleanup(server.Close)

			client = qdrant.NewClient(server.URL, "", &http.Client{Timeout: time.Millisecond})

			err := client.CreateCollection(context.Background(), "test", qdrant.CollectionConfig{
				VectorSize: 768,
				Distance:   "Cosine",
			})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("authentication", func() {
		It("sends api-key header when apiKey is non-empty", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.Header.Get("api-key")).To(Equal("test-secret-key"))
				w.WriteHeader(http.StatusOK)
			}))
			DeferCleanup(server.Close)

			client = qdrant.NewClient(server.URL, "test-secret-key", server.Client())

			err := client.CreateCollection(context.Background(), "test", qdrant.CollectionConfig{
				VectorSize: 768,
				Distance:   "Cosine",
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("EnsureCollection", func() {
		Context("when the collection is missing", func() {
			It("creates it with the supplied dim and distance", func() {
				var createdBody []byte
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch {
					case r.Method == http.MethodGet:
						w.WriteHeader(http.StatusNotFound)
						_, _ = w.Write([]byte(`{"status":"not found"}`))
					case r.Method == http.MethodPut:
						createdBody, _ = io.ReadAll(r.Body)
						w.WriteHeader(http.StatusOK)
						_, _ = w.Write([]byte(`{}`))
					}
				}))
				DeferCleanup(server.Close)
				client = qdrant.NewClient(server.URL, "", server.Client())

				Expect(client.EnsureCollection(context.Background(), "auto", 768, "Cosine")).To(Succeed())

				var payload map[string]any
				Expect(json.Unmarshal(createdBody, &payload)).To(Succeed())
				Expect(payload).To(HaveKey("vectors"))
			})
		})

		Context("when the collection already exists", func() {
			It("does not issue a second PUT", func() {
				var puts int
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.Method {
					case http.MethodGet:
						w.WriteHeader(http.StatusOK)
						_, _ = w.Write([]byte(`{}`))
					case http.MethodPut:
						puts++
						w.WriteHeader(http.StatusOK)
						_, _ = w.Write([]byte(`{}`))
					}
				}))
				DeferCleanup(server.Close)
				client = qdrant.NewClient(server.URL, "", server.Client())

				Expect(client.EnsureCollection(context.Background(), "auto", 768, "Cosine")).To(Succeed())
				Expect(puts).To(Equal(0))
			})
		})
	})
})

var _ = Describe("IsCollectionNotFound", func() {
	It("returns true for a wrapped *Error with status 404", func() {
		err := &qdrant.Error{StatusCode: 404, Message: "missing"}
		Expect(qdrant.IsCollectionNotFound(err)).To(BeTrue())
	})

	It("returns false for a non-404 *Error", func() {
		err := &qdrant.Error{StatusCode: 500, Message: "boom"}
		Expect(qdrant.IsCollectionNotFound(err)).To(BeFalse())
	})

	It("returns false for unrelated errors", func() {
		Expect(qdrant.IsCollectionNotFound(errors.New("boom"))).To(BeFalse())
		Expect(qdrant.IsCollectionNotFound(nil)).To(BeFalse())
	})
})
