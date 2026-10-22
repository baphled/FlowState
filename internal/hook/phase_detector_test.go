package hook_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"context"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("PhaseDetect", func() {
	DescribeTable("DetectPhase",
		func(text string, expected hook.PlanPhase) {
			Expect(hook.DetectPhase(text)).To(Equal(expected))
		},
		Entry("generation: plan with frontmatter", "---\nid: test\n---\n## Task", hook.PhaseGeneration),
		Entry("generation: complex frontmatter", "---\nid: task-1\nname: My Task\n---\nContent here", hook.PhaseGeneration),
		Entry("interview: question without frontmatter", "What should I build?", hook.PhaseInterview),
		Entry("interview: plain text", "Tell me about Go", hook.PhaseInterview),
		Entry("unknown: empty string", "", hook.PhaseUnknown),
		Entry("unknown: only whitespace", "   \n\n  ", hook.PhaseUnknown),
	)

	Describe("PhaseDetectorHook", func() {
		harnessManifest := func() agent.Manifest {
			return agent.Manifest{HarnessEnabled: true}
		}

		nonHarnessManifest := func() agent.Manifest {
			return agent.Manifest{HarnessEnabled: false}
		}

		It("stores detected phase in context when harness is enabled", func() {
			phaseDetectorHook := hook.PhaseDetectorHook(harnessManifest)
			baseHandler := func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				phase := hook.PhaseFromContext(ctx)
				Expect(phase).To(Equal(hook.PhaseGeneration))
				return make(chan provider.StreamChunk), nil
			}

			wrappedHandler := phaseDetectorHook(baseHandler)
			req := &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "user", Content: "---\nid: test\n---\nContent"},
				},
			}

			_, err := wrappedHandler(context.Background(), req)
			Expect(err).NotTo(HaveOccurred())
		})

		It("detects interview phase and stores in context", func() {
			phaseDetectorHook := hook.PhaseDetectorHook(harnessManifest)
			baseHandler := func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				phase := hook.PhaseFromContext(ctx)
				Expect(phase).To(Equal(hook.PhaseInterview))
				return make(chan provider.StreamChunk), nil
			}

			wrappedHandler := phaseDetectorHook(baseHandler)
			req := &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "user", Content: "What is the best way to learn Go?"},
				},
			}

			_, err := wrappedHandler(context.Background(), req)
			Expect(err).NotTo(HaveOccurred())
		})

		It("passes through to next handler when harness is enabled", func() {
			phaseDetectorHook := hook.PhaseDetectorHook(harnessManifest)
			called := false
			baseHandler := func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				called = true
				return make(chan provider.StreamChunk), nil
			}

			wrappedHandler := phaseDetectorHook(baseHandler)
			req := &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "user", Content: "test"},
				},
			}

			_, err := wrappedHandler(context.Background(), req)
			Expect(err).NotTo(HaveOccurred())
			Expect(called).To(BeTrue())
		})

		It("is a no-op when harness is disabled", func() {
			phaseDetectorHook := hook.PhaseDetectorHook(nonHarnessManifest)
			baseHandler := func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				phase := hook.PhaseFromContext(ctx)
				Expect(phase).To(Equal(hook.PhaseUnknown))
				return make(chan provider.StreamChunk), nil
			}

			wrappedHandler := phaseDetectorHook(baseHandler)
			req := &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "user", Content: "---\nid: test\n---\nContent"},
				},
			}

			_, err := wrappedHandler(context.Background(), req)
			Expect(err).NotTo(HaveOccurred())
		})

		It("evaluates harness status at request time not build time", func() {
			enabled := false
			dynamicManifest := func() agent.Manifest {
				return agent.Manifest{HarnessEnabled: enabled}
			}

			phaseDetectorHook := hook.PhaseDetectorHook(dynamicManifest)
			req := &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "user", Content: "---\nid: test\n---\nContent"},
				},
			}

			baseHandler := func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				phase := hook.PhaseFromContext(ctx)
				Expect(phase).To(Equal(hook.PhaseUnknown))
				return make(chan provider.StreamChunk), nil
			}
			wrappedHandler := phaseDetectorHook(baseHandler)
			_, err := wrappedHandler(context.Background(), req)
			Expect(err).NotTo(HaveOccurred())

			enabled = true
			activeHandler := func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				phase := hook.PhaseFromContext(ctx)
				Expect(phase).To(Equal(hook.PhaseGeneration))
				return make(chan provider.StreamChunk), nil
			}
			wrappedHandler = phaseDetectorHook(activeHandler)
			_, err = wrappedHandler(context.Background(), req)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
