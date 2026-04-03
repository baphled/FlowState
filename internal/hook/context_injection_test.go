package hook_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("ContextInjection", func() {
	makeManifest := func(id string, harnessEnabled bool) func() agent.Manifest {
		return func() agent.Manifest {
			return agent.Manifest{ID: id, HarnessEnabled: harnessEnabled}
		}
	}

	makeReq := func(hasAssistant bool) *provider.ChatRequest {
		msgs := []provider.Message{{Role: "user", Content: "help me plan"}}
		if hasAssistant {
			msgs = append(msgs, provider.Message{Role: "assistant", Content: "sure"})
		}
		return &provider.ChatRequest{Messages: msgs}
	}

	noop := hook.HandlerFunc(func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		ch := make(chan provider.StreamChunk)
		close(ch)
		return ch, nil
	})

	It("injects context for planner agent first message", func() {
		h := hook.ContextInjectionHook(makeManifest("planner", true), "/tmp")
		req := makeReq(false)
		_, err := h(noop)(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())
		Expect(req.Messages[0].Content).To(ContainSubstring("## Codebase Context"))
	})

	It("does not inject for non-planner agent", func() {
		h := hook.ContextInjectionHook(makeManifest("executor", true), "/tmp")
		req := makeReq(false)
		_, err := h(noop)(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())
		Expect(req.Messages[0].Content).NotTo(ContainSubstring("## Codebase Context"))
	})

	It("does not inject on continuation messages", func() {
		h := hook.ContextInjectionHook(makeManifest("planner", true), "/tmp")
		req := makeReq(true)
		_, err := h(noop)(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())
		Expect(req.Messages[0].Content).NotTo(ContainSubstring("## Codebase Context"))
	})

	It("is idempotent when already injected", func() {
		h := hook.ContextInjectionHook(makeManifest("planner", true), "/tmp")
		req := &provider.ChatRequest{
			Messages: []provider.Message{
				{Role: "system", Content: "## Codebase Context\nalready here"},
				{Role: "user", Content: "help"},
			},
		}
		_, err := h(noop)(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())
		count := strings.Count(req.Messages[0].Content, "## Codebase Context")
		Expect(count).To(Equal(1))
	})

	It("is a no-op when harness is disabled", func() {
		h := hook.ContextInjectionHook(makeManifest("planner", false), "/tmp")
		req := makeReq(false)
		_, err := h(noop)(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())
		Expect(req.Messages[0].Content).NotTo(ContainSubstring("## Codebase Context"))
	})

	It("evaluates harness status at request time not build time", func() {
		enabled := false
		dynamicManifest := func() agent.Manifest {
			return agent.Manifest{ID: "planner", HarnessEnabled: enabled}
		}

		h := hook.ContextInjectionHook(dynamicManifest, "/tmp")

		req := makeReq(false)
		_, err := h(noop)(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())
		Expect(req.Messages[0].Content).NotTo(ContainSubstring("## Codebase Context"))

		enabled = true
		req = makeReq(false)
		_, err = h(noop)(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())
		Expect(req.Messages[0].Content).To(ContainSubstring("## Codebase Context"))
	})
})
