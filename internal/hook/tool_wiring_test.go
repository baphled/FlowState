package hook_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("ToolWiringHook", func() {
	var (
		ctx               context.Context
		req               *provider.ChatRequest
		manifest          agent.Manifest
		hasToolResult     bool
		ensureToolsCalled bool
		ensuredManifest   agent.Manifest
		schemas           []provider.Tool
		capturedReq       *provider.ChatRequest
		nextCalled        bool
	)

	passthrough := func(ctx context.Context, r *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		nextCalled = true
		capturedReq = r
		ch := make(chan provider.StreamChunk, 1)
		ch <- provider.StreamChunk{Content: "ok", Done: true}
		close(ch)
		return ch, nil
	}

	BeforeEach(func() {
		ctx = context.Background()
		nextCalled = false
		ensureToolsCalled = false
		ensuredManifest = agent.Manifest{}
		capturedReq = nil
		hasToolResult = false

		schemas = []provider.Tool{
			{Name: "delegate", Description: "Delegate tasks"},
			{Name: "background_output", Description: "Get background output"},
		}

		req = &provider.ChatRequest{
			Messages: []provider.Message{
				{Role: "system", Content: "You are a helpful assistant."},
				{Role: "user", Content: "Hello"},
			},
			Tools: []provider.Tool{
				{Name: "bash", Description: "Run commands"},
			},
		}
	})

	buildHook := func() hook.Hook {
		return hook.ToolWiringHook(
			func() agent.Manifest { return manifest },
			func(name string) bool { return hasToolResult },
			func(m agent.Manifest) {
				ensureToolsCalled = true
				ensuredManifest = m
			},
			func() []provider.Tool { return schemas },
		)
	}

	Context("when the agent does not permit delegation", func() {
		BeforeEach(func() {
			manifest = agent.Manifest{
				ID:   "worker-agent",
				Name: "Worker",
				Delegation: agent.Delegation{
					CanDelegate: false,
				},
			}
		})

		It("skips tool wiring entirely", func() {
			wrapped := buildHook()(passthrough)

			_, err := wrapped(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(ensureToolsCalled).To(BeFalse())
		})

		It("does not modify req.Tools", func() {
			originalTools := make([]provider.Tool, len(req.Tools))
			copy(originalTools, req.Tools)

			wrapped := buildHook()(passthrough)

			_, err := wrapped(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(capturedReq.Tools).To(Equal(originalTools))
		})

		It("calls through to the next handler", func() {
			wrapped := buildHook()(passthrough)

			_, err := wrapped(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(nextCalled).To(BeTrue())
		})
	})

	Context("when the agent permits delegation and delegate tool is missing", func() {
		BeforeEach(func() {
			manifest = agent.Manifest{
				ID:   "orchestrator",
				Name: "Orchestrator",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}
			hasToolResult = false
		})

		It("calls ensureTools to wire delegation tools", func() {
			wrapped := buildHook()(passthrough)

			_, err := wrapped(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(ensureToolsCalled).To(BeTrue())
		})

		It("passes the current manifest to ensureTools", func() {
			wrapped := buildHook()(passthrough)

			_, err := wrapped(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(ensuredManifest.ID).To(Equal("orchestrator"))
		})

		It("sets req.Tools to the schema rebuilder output", func() {
			wrapped := buildHook()(passthrough)

			_, err := wrapped(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(capturedReq.Tools).To(Equal(schemas))
		})
	})

	Context("when the agent permits delegation and delegate tool already exists", func() {
		BeforeEach(func() {
			manifest = agent.Manifest{
				ID:   "orchestrator",
				Name: "Orchestrator",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}
			hasToolResult = true
		})

		It("does not call ensureTools again", func() {
			wrapped := buildHook()(passthrough)

			_, err := wrapped(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(ensureToolsCalled).To(BeFalse())
		})

		It("still refreshes req.Tools from the schema rebuilder", func() {
			wrapped := buildHook()(passthrough)

			_, err := wrapped(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(capturedReq.Tools).To(Equal(schemas))
		})
	})

	Context("when the next handler receives the modified request", func() {
		BeforeEach(func() {
			manifest = agent.Manifest{
				ID:   "orchestrator",
				Name: "Orchestrator",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}
			hasToolResult = true
		})

		It("sees the updated tool schemas", func() {
			var receivedTools []provider.Tool
			handler := func(_ context.Context, r *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				receivedTools = r.Tools
				ch := make(chan provider.StreamChunk, 1)
				ch <- provider.StreamChunk{Content: "ok", Done: true}
				close(ch)
				return ch, nil
			}

			wrapped := buildHook()(handler)

			_, err := wrapped(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(receivedTools).To(HaveLen(2))
			Expect(receivedTools[0].Name).To(Equal("delegate"))
			Expect(receivedTools[1].Name).To(Equal("background_output"))
		})
	})

	Context("when the schema rebuilder returns different schemas", func() {
		BeforeEach(func() {
			manifest = agent.Manifest{
				ID:   "orchestrator",
				Name: "Orchestrator",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}
			hasToolResult = true
		})

		It("propagates the exact schema rebuilder output to req.Tools", func() {
			customSchemas := []provider.Tool{
				{Name: "delegate", Description: "Custom delegate"},
				{Name: "background_output", Description: "Custom background"},
				{Name: "background_cancel", Description: "Custom cancel"},
			}

			wiringHook := hook.ToolWiringHook(
				func() agent.Manifest { return manifest },
				func(name string) bool { return true },
				func(m agent.Manifest) {},
				func() []provider.Tool { return customSchemas },
			)
			wrapped := wiringHook(passthrough)

			_, err := wrapped(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(capturedReq.Tools).To(Equal(customSchemas))
			Expect(capturedReq.Tools).To(HaveLen(3))
		})
	})
})
