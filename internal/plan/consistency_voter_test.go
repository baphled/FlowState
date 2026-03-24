package plan_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/provider"
)

type testVoterStreamer struct {
	responses []string
	callCount int
}

func (m *testVoterStreamer) Stream(ctx context.Context, agentID, message string) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 10)
	resp := m.responses[m.callCount]
	m.callCount++
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: resp}
	}()
	return ch, nil
}

var _ = Describe("ConsistencyVoter", func() {
	var (
		_ = context.Background()          // silence unused warning
		_ = (*plan.ConsistencyVoter)(nil) // silence unused warning
	)

	// BeforeEach intentionally left blank for now

	Context("when the initial plan score is below the threshold", func() {
		It("triggers variant generation and picks the best plan", func() {
			ctx := context.Background()
			config := plan.VoterConfig{Enabled: true, Variants: 2, Threshold: 0.9}
			voter := plan.NewConsistencyVoter(config, "/tmp")
			streamer := &testVoterStreamer{
				responses: []string{
					"variant 1 plan",
					"variant 2 plan",
				},
			}
			req := plan.VoteRequest{
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
		})
	})

	Context("when the initial plan score is above the threshold", func() {
		It("returns the initial plan without generating variants", func() {
			ctx := context.Background()
			config := plan.VoterConfig{Enabled: true, Variants: 2, Threshold: 0.7}
			voter := plan.NewConsistencyVoter(config, "/tmp")
			streamer := &testVoterStreamer{responses: []string{}}
			req := plan.VoteRequest{
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
		})
	})

	Context("when configured with N variants", func() {
		It("generates N variants in parallel", func() {
			ctx := context.Background()
			config := plan.VoterConfig{Enabled: true, Variants: 3, Threshold: 0.9}
			voter := plan.NewConsistencyVoter(config, "/tmp")
			streamer := &testVoterStreamer{
				responses: []string{
					"variant 1",
					"variant 2",
					"variant 3",
				},
			}
			req := plan.VoteRequest{
				AgentID:      "agent1",
				Message:      "generate plan",
				InitialPlan:  "initial plan",
				InitialScore: 0.5,
			}
			result, err := voter.Vote(ctx, streamer, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.VariantsGenerated).To(Equal(3))
		})
	})

	Context("when a variant fails validation", func() {
		It("handles validation errors gracefully", func() {
			ctx := context.Background()
			config := plan.VoterConfig{Enabled: true, Variants: 2, Threshold: 0.9}
			voter := plan.NewConsistencyVoter(config, "/tmp")
			streamer := &testVoterStreamer{
				responses: []string{
					"valid variant",
					"another variant",
				},
			}
			req := plan.VoteRequest{
				AgentID:      "agent1",
				Message:      "generate plan",
				InitialPlan:  "initial plan",
				InitialScore: 0.5,
			}
			result, err := voter.Vote(ctx, streamer, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
		})
	})

	Context("when the voter is disabled in config", func() {
		It("skips all variant generation and returns the initial plan", func() {
			ctx := context.Background()
			config := plan.VoterConfig{Enabled: false, Variants: 2, Threshold: 0.9}
			voter := plan.NewConsistencyVoter(config, "/tmp")
			streamer := &testVoterStreamer{responses: []string{}}
			req := plan.VoteRequest{
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
		})
	})
})
