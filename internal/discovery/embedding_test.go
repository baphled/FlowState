package discovery_test

import (
	"context"
	"math"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/discovery"
	"github.com/baphled/flowstate/internal/provider"
)

type fixedEmbedder struct {
	vectors map[string][]float64
}

func (f *fixedEmbedder) Embed(_ context.Context, req provider.EmbedRequest) ([]float64, error) {
	if v, ok := f.vectors[req.Input]; ok {
		return v, nil
	}
	return []float64{0, 0, 0}, nil
}

var _ = Describe("EmbeddingDiscovery", func() {
	var (
		registry *agent.Registry
		embedder *fixedEmbedder
	)

	BeforeEach(func() {
		registry = agent.NewRegistry()
		embedder = &fixedEmbedder{
			vectors: map[string][]float64{
				"writes Go code and implements features": {1, 0, 0},
				"investigates and analyses systems":      {0, 1, 0},
				"write Go code":                          {1, 0, 0},
			},
		}
	})

	Describe("Match", func() {
		Context("when agents are indexed with distinct capability vectors", func() {
			BeforeEach(func() {
				coderManifest := &agent.Manifest{
					ID:   "coder-agent",
					Name: "Coder",
					Capabilities: agent.Capabilities{
						CapabilityDescription: "writes Go code and implements features",
					},
				}
				researcherManifest := &agent.Manifest{
					ID:   "researcher-agent",
					Name: "Researcher",
					Capabilities: agent.Capabilities{
						CapabilityDescription: "investigates and analyses systems",
					},
				}
				registry.Register(coderManifest)
				registry.Register(researcherManifest)

				ed := discovery.NewEmbeddingDiscovery(registry, embedder)
				err := ed.IndexAgents(context.Background())
				Expect(err).NotTo(HaveOccurred())

				DeferCleanup(func() {})
			})

			It("returns agents ranked by cosine similarity", func() {
				ed := discovery.NewEmbeddingDiscovery(registry, embedder)
				err := ed.IndexAgents(context.Background())
				Expect(err).NotTo(HaveOccurred())

				embedder.vectors["write Go code"] = []float64{1, 0, 0}

				matches, err := ed.Match(context.Background(), "write Go code")
				Expect(err).NotTo(HaveOccurred())
				Expect(matches).NotTo(BeEmpty())
				Expect(matches[0].AgentID).To(Equal("coder-agent"))
				Expect(matches[0].Confidence).To(BeNumerically(">", 0))

				if len(matches) > 1 {
					Expect(matches[0].Confidence).To(BeNumerically(">=", matches[1].Confidence))
				}
			})
		})

		Context("when embedder is nil", func() {
			It("returns an empty slice", func() {
				ed := discovery.NewEmbeddingDiscovery(registry, nil)
				matches, err := ed.Match(context.Background(), "write Go code")
				Expect(err).NotTo(HaveOccurred())
				Expect(matches).To(BeEmpty())
			})
		})

		Context("when agent has aliases", func() {
			It("matches on alias terms via concatenated index text", func() {
				aliasEmbedder := &fixedEmbedder{
					vectors: map[string][]float64{
						"investigates and analyses systems research investigation": {0, 1, 0},
						"investigation": {0, 1, 0},
					},
				}

				m := &agent.Manifest{
					ID:      "researcher-agent",
					Name:    "Researcher",
					Aliases: []string{"research", "investigation"},
					Capabilities: agent.Capabilities{
						CapabilityDescription: "investigates and analyses systems",
					},
				}
				reg := agent.NewRegistry()
				reg.Register(m)

				ed := discovery.NewEmbeddingDiscovery(reg, aliasEmbedder)
				err := ed.IndexAgents(context.Background())
				Expect(err).NotTo(HaveOccurred())

				matches, err := ed.Match(context.Background(), "investigation")
				Expect(err).NotTo(HaveOccurred())
				Expect(matches).NotTo(BeEmpty())
				Expect(matches[0].AgentID).To(Equal("researcher-agent"))
				Expect(matches[0].Confidence).To(BeNumerically(">", 0.9))
			})
		})

		Context("when confidence is below 0.7", func() {
			It("still returns results and lets the caller decide on threshold", func() {
				lowEmbedder := &fixedEmbedder{
					vectors: map[string][]float64{
						"agent description": {1, 0, 0},
						"unrelated query":   {0.5, 0.866, 0},
					},
				}

				m := &agent.Manifest{
					ID:   "some-agent",
					Name: "Some Agent",
					Capabilities: agent.Capabilities{
						CapabilityDescription: "agent description",
					},
				}
				reg := agent.NewRegistry()
				reg.Register(m)

				ed := discovery.NewEmbeddingDiscovery(reg, lowEmbedder)
				err := ed.IndexAgents(context.Background())
				Expect(err).NotTo(HaveOccurred())

				matches, err := ed.Match(context.Background(), "unrelated query")
				Expect(err).NotTo(HaveOccurred())
				Expect(matches).NotTo(BeEmpty())
				Expect(matches[0].Confidence).To(BeNumerically("<", 0.7))
			})
		})
	})

	Describe("concurrent access", func() {
		Context("when IndexAgents and Match are called concurrently", func() {
			It("does not race on agentVecs", func() {
				reg := agent.NewRegistry()
				reg.Register(&agent.Manifest{
					ID:   "concurrent-agent",
					Name: "Concurrent Agent",
					Capabilities: agent.Capabilities{
						CapabilityDescription: "writes Go code and implements features",
					},
				})

				em := &fixedEmbedder{
					vectors: map[string][]float64{
						"writes Go code and implements features": {1, 0, 0},
						"write Go code":                          {1, 0, 0},
					},
				}
				ed := discovery.NewEmbeddingDiscovery(reg, em)

				var wg sync.WaitGroup
				for range 10 {
					wg.Add(2)
					go func() {
						defer wg.Done()
						_ = ed.IndexAgents(context.Background())
					}()
					go func() {
						defer wg.Done()
						_, _ = ed.Match(context.Background(), "write Go code")
					}()
				}
				wg.Wait()
			})
		})
	})

	Describe("CosineSimilarity", func() {
		Context("when vectors are identical", func() {
			It("returns 1.0", func() {
				a := []float64{1, 2, 3}
				score := discovery.CosineSimilarity(a, a)
				Expect(score).To(BeNumerically("~", 1.0, 1e-9))
			})
		})

		Context("when vectors are orthogonal", func() {
			It("returns 0.0", func() {
				a := []float64{1, 0, 0}
				b := []float64{0, 1, 0}
				score := discovery.CosineSimilarity(a, b)
				Expect(score).To(BeNumerically("~", 0.0, 1e-9))
			})
		})

		Context("when vectors are of different lengths", func() {
			It("returns 0.0", func() {
				a := []float64{1, 2}
				b := []float64{1, 2, 3}
				score := discovery.CosineSimilarity(a, b)
				Expect(score).To(Equal(0.0))
			})
		})

		Context("when vectors are empty", func() {
			It("returns 0.0", func() {
				score := discovery.CosineSimilarity([]float64{}, []float64{})
				Expect(score).To(Equal(0.0))
			})
		})

		Context("when vectors are at 60 degrees", func() {
			It("returns 0.5", func() {
				a := []float64{1, 0}
				b := []float64{0.5, math.Sqrt(3) / 2}
				score := discovery.CosineSimilarity(a, b)
				Expect(score).To(BeNumerically("~", 0.5, 1e-9))
			})
		})
	})
})
