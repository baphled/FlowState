package qdrant_test

import (
	"context"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/recall/qdrant"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Qdrant Client external", Label("external"), func() {
	var (
		client     *qdrant.Client
		collection string
	)

	BeforeEach(func() {
		qdrantURL := os.Getenv("QDRANT_URL")
		if qdrantURL == "" {
			Skip("QDRANT_URL not set — skipping external tests")
		}
		client = qdrant.NewClient(qdrantURL, "", nil)
		collection = fmt.Sprintf("test-%d", GinkgoRandomSeed())
	})

	AfterEach(func() {
		if client != nil {
			_ = client.DeleteCollection(context.Background(), collection)
		}
	})

	It("creates a collection and confirms it exists", func() {
		err := client.CreateCollection(context.Background(), collection, qdrant.CollectionConfig{
			VectorSize: 4,
			Distance:   "Cosine",
		})
		Expect(err).NotTo(HaveOccurred())

		exists, err := client.CollectionExists(context.Background(), collection)
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeTrue())
	})

	It("upserts and searches with deterministic unit vectors", func() {
		err := client.CreateCollection(context.Background(), collection, qdrant.CollectionConfig{
			VectorSize: 4,
			Distance:   "Cosine",
		})
		Expect(err).NotTo(HaveOccurred())

		point := qdrant.Point{
			ID:      "ext-001",
			Vector:  []float64{1.0, 0.0, 0.0, 0.0},
			Payload: map[string]any{"content": "external test content"},
		}
		err = client.Upsert(context.Background(), collection, []qdrant.Point{point}, true)
		Expect(err).NotTo(HaveOccurred())

		results, err := client.Search(context.Background(), collection, []float64{1.0, 0.0, 0.0, 0.0}, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].ID).To(Equal("ext-001"))
		Expect(results[0].Score).To(BeNumerically(">=", 0.99))
	})

	It("deletes a collection", func() {
		err := client.CreateCollection(context.Background(), collection, qdrant.CollectionConfig{
			VectorSize: 4,
			Distance:   "Cosine",
		})
		Expect(err).NotTo(HaveOccurred())

		err = client.DeleteCollection(context.Background(), collection)
		Expect(err).NotTo(HaveOccurred())

		exists, err := client.CollectionExists(context.Background(), collection)
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeFalse())
	})
})
