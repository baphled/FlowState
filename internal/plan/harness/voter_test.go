package harness_test

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plan/harness"
	"github.com/baphled/flowstate/internal/provider"
)

type testVoterStreamer struct {
	mu        sync.Mutex
	responses []string
	callCount int
}

func (m *testVoterStreamer) Stream(_ context.Context, _, _ string) (<-chan provider.StreamChunk, error) {
	m.mu.Lock()
	idx := m.callCount
	if idx >= len(m.responses) {
		idx = len(m.responses) - 1
	}
	resp := m.responses[idx]
	m.callCount++
	m.mu.Unlock()
	ch := make(chan provider.StreamChunk, 10)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: resp}
	}()
	return ch, nil
}

var _ = Describe("ConsistencyVoter", func() {
	Context("when the initial plan score is below the threshold", func() {
		It("triggers variant generation and picks the best plan", func() {
			ctx := context.Background()
			config := harness.VoterConfig{Enabled: true, Variants: 2, Threshold: 0.9}
			voter := harness.NewConsistencyVoter(config, "/tmp")
			streamer := &testVoterStreamer{
				responses: []string{
					"variant 1 plan",
					"variant 2 plan",
				},
			}
			req := harness.VoteRequest{
				AgentID:      "agent1",
				Message:      "generate plan",
				InitialPlan:  "initial plan",
				InitialScore: 0.5,
			}
			result, err := voter.Vote(ctx, streamer, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.WasTriggered).To(BeTrue())
			Expect(result.VariantsGenerated).To(Equal(2))
			Expect(result.BestPlan).NotTo(Equal("initial plan"))
		})
	})

	Context("when the initial plan score is above the threshold", func() {
		It("returns the initial plan without generating variants", func() {
			ctx := context.Background()
			config := harness.VoterConfig{Enabled: true, Variants: 2, Threshold: 0.7}
			voter := harness.NewConsistencyVoter(config, "/tmp")
			streamer := &testVoterStreamer{responses: []string{}}
			req := harness.VoteRequest{
				AgentID:      "agent1",
				Message:      "generate plan",
				InitialPlan:  "initial plan",
				InitialScore: 0.95,
			}
			result, err := voter.Vote(ctx, streamer, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.WasTriggered).To(BeFalse())
			Expect(result.BestPlan).To(Equal("initial plan"))
			Expect(result.VariantsGenerated).To(Equal(0))
		})
	})

	Context("when the score equals the threshold exactly", func() {
		It("does not trigger variant generation", func() {
			ctx := context.Background()
			config := harness.VoterConfig{Enabled: true, Variants: 2, Threshold: 0.8}
			voter := harness.NewConsistencyVoter(config, "/tmp")
			streamer := &testVoterStreamer{responses: []string{}}
			req := harness.VoteRequest{
				AgentID:      "agent1",
				Message:      "generate plan",
				InitialPlan:  "initial plan",
				InitialScore: 0.8,
			}
			result, err := voter.Vote(ctx, streamer, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.WasTriggered).To(BeFalse())
			Expect(result.BestPlan).To(Equal("initial plan"))
		})
	})

	Context("when configured with N variants", func() {
		It("generates N variants in parallel", func() {
			ctx := context.Background()
			config := harness.VoterConfig{Enabled: true, Variants: 3, Threshold: 0.9}
			voter := harness.NewConsistencyVoter(config, "/tmp")
			streamer := &testVoterStreamer{
				responses: []string{
					"variant 1",
					"variant 2",
					"variant 3",
				},
			}
			req := harness.VoteRequest{
				AgentID:      "agent1",
				Message:      "generate plan",
				InitialPlan:  "initial plan",
				InitialScore: 0.5,
			}
			result, err := voter.Vote(ctx, streamer, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.VariantsGenerated).To(Equal(3))
			Expect(result.BestPlan).NotTo(BeEmpty())
		})
	})

	Context("when variants are generated", func() {
		It("selects a variant as best plan over the initial", func() {
			ctx := context.Background()
			config := harness.VoterConfig{Enabled: true, Variants: 2, Threshold: 0.9}
			voter := harness.NewConsistencyVoter(config, "/tmp")
			streamer := &testVoterStreamer{
				responses: []string{
					"better variant plan",
					"another variant plan",
				},
			}
			req := harness.VoteRequest{
				AgentID:      "agent1",
				Message:      "generate plan",
				InitialPlan:  "initial plan",
				InitialScore: 0.5,
			}
			result, err := voter.Vote(ctx, streamer, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.WasTriggered).To(BeTrue())
			Expect(result.BestPlan).To(BeElementOf("better variant plan", "another variant plan"))
		})
	})

	Context("when the voter is disabled in config", func() {
		It("skips all variant generation and returns the initial plan", func() {
			ctx := context.Background()
			config := harness.VoterConfig{Enabled: false, Variants: 2, Threshold: 0.9}
			voter := harness.NewConsistencyVoter(config, "/tmp")
			streamer := &testVoterStreamer{responses: []string{}}
			req := harness.VoteRequest{
				AgentID:      "agent1",
				Message:      "generate plan",
				InitialPlan:  "initial plan",
				InitialScore: 0.5,
			}
			result, err := voter.Vote(ctx, streamer, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.WasTriggered).To(BeFalse())
			Expect(result.BestPlan).To(Equal("initial plan"))
			Expect(result.VariantsGenerated).To(Equal(0))
		})
	})

	Context("when DefaultVoterConfig is used", func() {
		It("returns sensible defaults", func() {
			config := harness.DefaultVoterConfig()
			Expect(config.Enabled).To(BeFalse())
			Expect(config.Variants).To(Equal(3))
			Expect(config.Threshold).To(Equal(0.8))
		})
	})
})
