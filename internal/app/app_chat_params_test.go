// Package app_test provides Ginkgo specs for the app package.
package app_test

import (
	"context"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/plugin"
	"github.com/baphled/flowstate/internal/plugin/external"
	"github.com/baphled/flowstate/internal/provider"
)

type chatParamsMockPlugin struct {
	name    string
	version string
	hooks   map[plugin.HookType]interface{}
}

func (m *chatParamsMockPlugin) Init() error                            { return nil }
func (m *chatParamsMockPlugin) Name() string                           { return m.name }
func (m *chatParamsMockPlugin) Version() string                        { return m.version }
func (m *chatParamsMockPlugin) Hooks() map[plugin.HookType]interface{} { return m.hooks }

var _ = Describe("chat.params dispatch path in buildHookChain", func() {
	var (
		reg        *plugin.Registry
		dispatcher *external.Dispatcher
		callCount  int32
		sentReq    *provider.ChatRequest
	)

	BeforeEach(func() {
		callCount = 0
		sentReq = nil
		reg = plugin.NewRegistry()
		dispatcher = external.NewDispatcher(reg)
	})

	Describe("BuildHookChainWithDispatcherForTest", func() {
		Context("when a dispatcher with a chat.params plugin is provided", func() {
			It("dispatches the ChatRequest to registered plugins", func() {
				p := &chatParamsMockPlugin{
					name:    "test-chat-params-plugin",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, req *provider.ChatRequest) error {
							atomic.AddInt32(&callCount, 1)
							sentReq = req
							return nil
						}),
					},
				}
				Expect(reg.Register(p)).To(Succeed())

				manifestGetter := func() agent.Manifest { return agent.Manifest{} }
				chain := app.BuildHookChainWithDispatcherForTest(nil, manifestGetter, dispatcher)

				req := &provider.ChatRequest{Model: "test-model"}
				_, _ = chain.Execute(func(_ context.Context, _ *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
					ch := make(chan provider.StreamChunk)
					close(ch)
					return ch, nil
				})(context.Background(), req)

				Expect(atomic.LoadInt32(&callCount)).To(Equal(int32(1)))
				Expect(sentReq).To(Equal(req))
			})

			It("includes one additional hook compared to no-dispatcher chain", func() {
				manifestGetter := func() agent.Manifest { return agent.Manifest{} }
				withDispatcher := app.BuildHookChainWithDispatcherForTest(nil, manifestGetter, dispatcher)
				withoutDispatcher := app.BuildHookChainForTest(nil, manifestGetter)

				Expect(withDispatcher.Len()).To(Equal(withoutDispatcher.Len() + 1))
			})
		})

		Context("when dispatcher is nil", func() {
			It("skips the chat.params hook and chain length is unchanged", func() {
				manifestGetter := func() agent.Manifest { return agent.Manifest{} }
				withNilDispatcher := app.BuildHookChainWithDispatcherForTest(nil, manifestGetter, nil)
				withoutDispatcher := app.BuildHookChainForTest(nil, manifestGetter)

				Expect(withNilDispatcher.Len()).To(Equal(withoutDispatcher.Len()))
			})
		})

		Context("when a dispatcher with no chat.params plugin is provided", func() {
			It("does not call any plugin hook", func() {
				manifestGetter := func() agent.Manifest { return agent.Manifest{} }
				chain := app.BuildHookChainWithDispatcherForTest(nil, manifestGetter, dispatcher)

				req := &provider.ChatRequest{Model: "no-plugin-model"}
				_, _ = chain.Execute(func(_ context.Context, _ *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
					ch := make(chan provider.StreamChunk)
					close(ch)
					return ch, nil
				})(context.Background(), req)

				Expect(atomic.LoadInt32(&callCount)).To(BeZero())
			})
		})

		Context("when the plugin hook returns an error", func() {
			It("logs the error and continues to the next handler", func() {
				p := &chatParamsMockPlugin{
					name:    "erroring-chat-params",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
							atomic.AddInt32(&callCount, 1)
							return context.DeadlineExceeded
						}),
					},
				}
				Expect(reg.Register(p)).To(Succeed())

				manifestGetter := func() agent.Manifest { return agent.Manifest{} }
				chain := app.BuildHookChainWithDispatcherForTest(nil, manifestGetter, dispatcher)

				_, err := chain.Execute(func(_ context.Context, _ *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
					ch := make(chan provider.StreamChunk)
					close(ch)
					return ch, nil
				})(context.Background(), &provider.ChatRequest{})

				Expect(err).NotTo(HaveOccurred())
				Expect(atomic.LoadInt32(&callCount)).To(Equal(int32(1)))
			})
		})
	})
})
