package streaming_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
)

var _ = Describe("VerbosityLevel", func() {
	Describe("String representation", func() {
		It("returns minimal for Minimal level", func() {
			Expect(streaming.Minimal.String()).To(Equal("minimal"))
		})

		It("returns standard for Standard level", func() {
			Expect(streaming.Standard.String()).To(Equal("standard"))
		})

		It("returns verbose for Verbose level", func() {
			Expect(streaming.Verbose.String()).To(Equal("verbose"))
		})
	})
})

var _ = Describe("VerbosityFilter", func() {
	var filter *streaming.VerbosityFilter

	BeforeEach(func() {
		filter = &streaming.VerbosityFilter{Level: streaming.Standard}
	})

	Describe("ShouldEmit", func() {
		Context("at Minimal verbosity", func() {
			BeforeEach(func() {
				filter.Level = streaming.Minimal
			})

			It("emits StatusTransitionEvent", func() {
				event := &streaming.StatusTransitionEvent{
					From: "planning",
					To:   "executing",
				}
				Expect(filter.ShouldEmit(event)).To(BeTrue())
			})

			It("emits PlanArtifactEvent", func() {
				event := &streaming.PlanArtifactEvent{
					Content: "plan data",
					Format:  "json",
				}
				Expect(filter.ShouldEmit(event)).To(BeTrue())
			})

			It("emits ReviewVerdictEvent", func() {
				event := &streaming.ReviewVerdictEvent{
					Verdict:    "approve",
					Confidence: 0.9,
					Issues:     []string{},
				}
				Expect(filter.ShouldEmit(event)).To(BeTrue())
			})

			It("does not emit ToolCallEvent", func() {
				event := &streaming.ToolCallEvent{
					Name:     "test",
					Args:     map[string]any{},
					Result:   "done",
					Duration: 10 * time.Millisecond,
				}
				Expect(filter.ShouldEmit(event)).To(BeFalse())
			})

			It("does not emit DelegationEvent", func() {
				event := &streaming.DelegationEvent{
					Source:  "planner",
					Target:  "executor",
					ChainID: "123",
					Status:  "started",
				}
				Expect(filter.ShouldEmit(event)).To(BeFalse())
			})

			It("does not emit CoordinationStoreEvent", func() {
				event := &streaming.CoordinationStoreEvent{
					Operation: "get",
					Key:       "key1",
					ChainID:   "123",
				}
				Expect(filter.ShouldEmit(event)).To(BeFalse())
			})

			It("does not emit TextChunkEvent", func() {
				event := &streaming.TextChunkEvent{Content: "hello"}
				Expect(filter.ShouldEmit(event)).To(BeFalse())
			})
		})

		Context("at Standard verbosity", func() {
			BeforeEach(func() {
				filter.Level = streaming.Standard
			})

			It("emits all Minimal events", func() {
				Expect(filter.ShouldEmit(&streaming.StatusTransitionEvent{From: "a", To: "b"})).To(BeTrue())
				Expect(filter.ShouldEmit(&streaming.PlanArtifactEvent{Content: "x", Format: "y"})).To(BeTrue())
				Expect(filter.ShouldEmit(&streaming.ReviewVerdictEvent{Verdict: "approve", Confidence: 1.0, Issues: nil})).To(BeTrue())
			})

			It("emits ToolCallEvent", func() {
				event := &streaming.ToolCallEvent{
					Name:     "test",
					Args:     map[string]any{},
					Result:   "done",
					Duration: 10 * time.Millisecond,
				}
				Expect(filter.ShouldEmit(event)).To(BeTrue())
			})

			It("emits DelegationEvent", func() {
				event := &streaming.DelegationEvent{
					Source:  "planner",
					Target:  "executor",
					ChainID: "123",
					Status:  "started",
				}
				Expect(filter.ShouldEmit(event)).To(BeTrue())
			})

			It("does not emit CoordinationStoreEvent", func() {
				event := &streaming.CoordinationStoreEvent{
					Operation: "get",
					Key:       "key1",
					ChainID:   "123",
				}
				Expect(filter.ShouldEmit(event)).To(BeFalse())
			})

			It("does not emit TextChunkEvent", func() {
				event := &streaming.TextChunkEvent{Content: "hello"}
				Expect(filter.ShouldEmit(event)).To(BeFalse())
			})
		})

		Context("at Verbose verbosity", func() {
			BeforeEach(func() {
				filter.Level = streaming.Verbose
			})

			It("emits all event types", func() {
				Expect(filter.ShouldEmit(&streaming.StatusTransitionEvent{From: "a", To: "b"})).To(BeTrue())
				Expect(filter.ShouldEmit(&streaming.PlanArtifactEvent{Content: "x", Format: "y"})).To(BeTrue())
				Expect(filter.ShouldEmit(&streaming.ReviewVerdictEvent{Verdict: "approve", Confidence: 1.0, Issues: nil})).To(BeTrue())
				Expect(filter.ShouldEmit(&streaming.ToolCallEvent{Name: "test", Args: nil, Result: "", Duration: 0})).To(BeTrue())
				Expect(filter.ShouldEmit(&streaming.DelegationEvent{Source: "a", Target: "b", ChainID: "c", Status: "x"})).To(BeTrue())
				Expect(filter.ShouldEmit(&streaming.CoordinationStoreEvent{Operation: "get", Key: "k", ChainID: "c"})).To(BeTrue())
				Expect(filter.ShouldEmit(&streaming.TextChunkEvent{Content: "hello"})).To(BeTrue())
			})
		})
	})

	Describe("NewVerbosityFilter", func() {
		It("creates a filter with the specified level", func() {
			filter := streaming.NewVerbosityFilter(streaming.Verbose)
			Expect(filter.Level).To(Equal(streaming.Verbose))
		})
	})
})
