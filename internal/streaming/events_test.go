package streaming_test

import (
	"encoding/json"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
)

type spyEventConsumer struct {
	events []streaming.Event
	chunks []string
	errs   []error
	done   bool
}

func (s *spyEventConsumer) WriteEvent(e streaming.Event) error {
	s.events = append(s.events, e)
	return nil
}

func (s *spyEventConsumer) WriteChunk(content string) error {
	s.chunks = append(s.chunks, content)
	return nil
}

func (s *spyEventConsumer) WriteError(err error) {
	s.errs = append(s.errs, err)
}

func (s *spyEventConsumer) Done() {
	s.done = true
}

var _ = Describe("Events", func() {
	Describe("Event types", func() {
		DescribeTable("returns the correct type string",
			func(event streaming.Event, expectedType string) {
				Expect(event.Type()).To(Equal(expectedType))
			},
			Entry("TextChunkEvent", streaming.TextChunkEvent{}, "text_chunk"),
			Entry("ToolCallEvent", streaming.ToolCallEvent{}, "tool_call"),
			Entry("DelegationEvent", streaming.DelegationEvent{}, "delegation"),
			Entry("CoordinationStoreEvent", streaming.CoordinationStoreEvent{}, "coordination_store"),
			Entry("StatusTransitionEvent", streaming.StatusTransitionEvent{}, "status_transition"),
			Entry("PlanArtifactEvent", streaming.PlanArtifactEvent{}, "plan_artifact"),
			Entry("ReviewVerdictEvent", streaming.ReviewVerdictEvent{}, "review_verdict"),
		)
	})

	Describe("JSON serialisation", func() {
		DescribeTable("includes type discriminator field",
			func(event streaming.Event) {
				data, err := streaming.MarshalEvent(event)
				Expect(err).NotTo(HaveOccurred())

				var raw map[string]interface{}
				Expect(json.Unmarshal(data, &raw)).To(Succeed())
				Expect(raw).To(HaveKey("type"))
				Expect(raw["type"]).To(Equal(event.Type()))
			},
			Entry("TextChunkEvent", streaming.TextChunkEvent{Content: "hello", AgentID: "a1"}),
			Entry("ToolCallEvent", streaming.ToolCallEvent{Name: "bash", AgentID: "a1"}),
			Entry("DelegationEvent", streaming.DelegationEvent{SourceAgent: "s", TargetAgent: "t"}),
			Entry("CoordinationStoreEvent", streaming.CoordinationStoreEvent{Operation: "get", Key: "k"}),
			Entry("StatusTransitionEvent", streaming.StatusTransitionEvent{From: "idle", To: "running"}),
			Entry("PlanArtifactEvent", streaming.PlanArtifactEvent{Content: "plan", Format: "markdown"}),
			Entry("ReviewVerdictEvent", streaming.ReviewVerdictEvent{Verdict: "approve", Confidence: 0.95}),
		)

		It("round-trips TextChunkEvent preserving all fields", func() {
			original := streaming.TextChunkEvent{Content: "hello world", AgentID: "agent-1"}
			data, err := streaming.MarshalEvent(original)
			Expect(err).NotTo(HaveOccurred())

			restored, err := streaming.UnmarshalEvent(data)
			Expect(err).NotTo(HaveOccurred())
			Expect(restored).To(Equal(original))
		})

		It("round-trips ToolCallEvent preserving all fields", func() {
			original := streaming.ToolCallEvent{
				Name:      "bash",
				Arguments: map[string]interface{}{"cmd": "ls"},
				Result:    "file1 file2",
				Duration:  5 * time.Second,
				AgentID:   "agent-1",
			}
			data, err := streaming.MarshalEvent(original)
			Expect(err).NotTo(HaveOccurred())

			restored, err := streaming.UnmarshalEvent(data)
			Expect(err).NotTo(HaveOccurred())
			Expect(restored).To(Equal(original))
		})

		It("round-trips DelegationEvent preserving all fields", func() {
			original := streaming.DelegationEvent{
				SourceAgent: "orchestrator",
				TargetAgent: "worker",
				ChainID:     "chain-1",
				Status:      "started",
			}
			data, err := streaming.MarshalEvent(original)
			Expect(err).NotTo(HaveOccurred())

			restored, err := streaming.UnmarshalEvent(data)
			Expect(err).NotTo(HaveOccurred())
			Expect(restored).To(Equal(original))
		})

		It("round-trips CoordinationStoreEvent preserving all fields", func() {
			original := streaming.CoordinationStoreEvent{
				Operation: "set",
				Key:       "plan_result",
				ChainID:   "chain-1",
			}
			data, err := streaming.MarshalEvent(original)
			Expect(err).NotTo(HaveOccurred())

			restored, err := streaming.UnmarshalEvent(data)
			Expect(err).NotTo(HaveOccurred())
			Expect(restored).To(Equal(original))
		})

		It("round-trips StatusTransitionEvent preserving all fields", func() {
			original := streaming.StatusTransitionEvent{
				From:    "idle",
				To:      "running",
				AgentID: "agent-1",
			}
			data, err := streaming.MarshalEvent(original)
			Expect(err).NotTo(HaveOccurred())

			restored, err := streaming.UnmarshalEvent(data)
			Expect(err).NotTo(HaveOccurred())
			Expect(restored).To(Equal(original))
		})

		It("round-trips PlanArtifactEvent preserving all fields", func() {
			original := streaming.PlanArtifactEvent{
				Content: "## Plan\n- Step 1\n- Step 2",
				Format:  "markdown",
				AgentID: "planner",
			}
			data, err := streaming.MarshalEvent(original)
			Expect(err).NotTo(HaveOccurred())

			restored, err := streaming.UnmarshalEvent(data)
			Expect(err).NotTo(HaveOccurred())
			Expect(restored).To(Equal(original))
		})

		It("round-trips ReviewVerdictEvent preserving all fields", func() {
			original := streaming.ReviewVerdictEvent{
				Verdict:    "reject",
				Confidence: 0.85,
				Issues:     []string{"missing tests", "no docs"},
				AgentID:    "reviewer",
			}
			data, err := streaming.MarshalEvent(original)
			Expect(err).NotTo(HaveOccurred())

			restored, err := streaming.UnmarshalEvent(data)
			Expect(err).NotTo(HaveOccurred())
			Expect(restored).To(Equal(original))
		})

		It("returns an error for unknown event type", func() {
			data := []byte(`{"type":"unknown_event"}`)
			_, err := streaming.UnmarshalEvent(data)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unknown_event"))
		})
	})
})

var _ = Describe("VerbosityFilter", func() {
	var (
		spy    *spyEventConsumer
		filter *streaming.VerbosityFilter
	)

	Describe("interface compliance", func() {
		It("satisfies StreamConsumer", func() {
			var _ streaming.StreamConsumer = (*streaming.VerbosityFilter)(nil)
		})

		It("satisfies EventConsumer", func() {
			var _ streaming.EventConsumer = (*streaming.VerbosityFilter)(nil)
		})
	})

	Context("at Minimal level", func() {
		BeforeEach(func() {
			spy = &spyEventConsumer{}
			filter = streaming.NewVerbosityFilter(spy, streaming.Minimal)
		})

		It("passes StatusTransitionEvent", func() {
			Expect(filter.WriteEvent(streaming.StatusTransitionEvent{From: "a", To: "b"})).To(Succeed())
			Expect(spy.events).To(HaveLen(1))
		})

		It("passes PlanArtifactEvent", func() {
			Expect(filter.WriteEvent(streaming.PlanArtifactEvent{Content: "plan"})).To(Succeed())
			Expect(spy.events).To(HaveLen(1))
		})

		It("passes ReviewVerdictEvent", func() {
			Expect(filter.WriteEvent(streaming.ReviewVerdictEvent{Verdict: "approve"})).To(Succeed())
			Expect(spy.events).To(HaveLen(1))
		})

		It("blocks TextChunkEvent", func() {
			Expect(filter.WriteEvent(streaming.TextChunkEvent{Content: "hi"})).To(Succeed())
			Expect(spy.events).To(BeEmpty())
		})

		It("blocks ToolCallEvent", func() {
			Expect(filter.WriteEvent(streaming.ToolCallEvent{Name: "bash"})).To(Succeed())
			Expect(spy.events).To(BeEmpty())
		})

		It("blocks DelegationEvent", func() {
			Expect(filter.WriteEvent(streaming.DelegationEvent{SourceAgent: "s"})).To(Succeed())
			Expect(spy.events).To(BeEmpty())
		})

		It("blocks CoordinationStoreEvent", func() {
			Expect(filter.WriteEvent(streaming.CoordinationStoreEvent{Operation: "get"})).To(Succeed())
			Expect(spy.events).To(BeEmpty())
		})
	})

	Context("at Standard level", func() {
		BeforeEach(func() {
			spy = &spyEventConsumer{}
			filter = streaming.NewVerbosityFilter(spy, streaming.Standard)
		})

		It("passes StatusTransitionEvent", func() {
			Expect(filter.WriteEvent(streaming.StatusTransitionEvent{From: "a", To: "b"})).To(Succeed())
			Expect(spy.events).To(HaveLen(1))
		})

		It("passes PlanArtifactEvent", func() {
			Expect(filter.WriteEvent(streaming.PlanArtifactEvent{Content: "plan"})).To(Succeed())
			Expect(spy.events).To(HaveLen(1))
		})

		It("passes ReviewVerdictEvent", func() {
			Expect(filter.WriteEvent(streaming.ReviewVerdictEvent{Verdict: "approve"})).To(Succeed())
			Expect(spy.events).To(HaveLen(1))
		})

		It("passes ToolCallEvent", func() {
			Expect(filter.WriteEvent(streaming.ToolCallEvent{Name: "bash"})).To(Succeed())
			Expect(spy.events).To(HaveLen(1))
		})

		It("passes DelegationEvent", func() {
			Expect(filter.WriteEvent(streaming.DelegationEvent{SourceAgent: "s"})).To(Succeed())
			Expect(spy.events).To(HaveLen(1))
		})

		It("blocks TextChunkEvent", func() {
			Expect(filter.WriteEvent(streaming.TextChunkEvent{Content: "hi"})).To(Succeed())
			Expect(spy.events).To(BeEmpty())
		})

		It("blocks CoordinationStoreEvent", func() {
			Expect(filter.WriteEvent(streaming.CoordinationStoreEvent{Operation: "get"})).To(Succeed())
			Expect(spy.events).To(BeEmpty())
		})
	})

	Context("at Verbose level", func() {
		BeforeEach(func() {
			spy = &spyEventConsumer{}
			filter = streaming.NewVerbosityFilter(spy, streaming.Verbose)
		})

		DescribeTable("passes all event types",
			func(event streaming.Event) {
				Expect(filter.WriteEvent(event)).To(Succeed())
				Expect(spy.events).To(HaveLen(1))
			},
			Entry("TextChunkEvent", streaming.TextChunkEvent{Content: "hi"}),
			Entry("ToolCallEvent", streaming.ToolCallEvent{Name: "bash"}),
			Entry("DelegationEvent", streaming.DelegationEvent{SourceAgent: "s"}),
			Entry("CoordinationStoreEvent", streaming.CoordinationStoreEvent{Operation: "get"}),
			Entry("StatusTransitionEvent", streaming.StatusTransitionEvent{From: "a", To: "b"}),
			Entry("PlanArtifactEvent", streaming.PlanArtifactEvent{Content: "plan"}),
			Entry("ReviewVerdictEvent", streaming.ReviewVerdictEvent{Verdict: "approve"}),
		)
	})

	Describe("StreamConsumer passthrough", func() {
		BeforeEach(func() {
			spy = &spyEventConsumer{}
			filter = streaming.NewVerbosityFilter(spy, streaming.Minimal)
		})

		It("passes WriteChunk through unconditionally", func() {
			Expect(filter.WriteChunk("raw content")).To(Succeed())
			Expect(spy.chunks).To(Equal([]string{"raw content"}))
		})

		It("passes WriteError through unconditionally", func() {
			testErr := errors.New("test error")
			filter.WriteError(testErr)
			Expect(spy.errs).To(HaveLen(1))
			Expect(spy.errs[0]).To(MatchError("test error"))
		})

		It("passes Done through unconditionally", func() {
			filter.Done()
			Expect(spy.done).To(BeTrue())
		})
	})
})
