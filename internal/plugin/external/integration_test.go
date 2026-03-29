package external_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin"
	"github.com/baphled/flowstate/internal/plugin/external"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("Plugin System — Integration", func() {
	var (
		reg        *plugin.Registry
		dispatcher *external.Dispatcher
		ctx        context.Context
	)

	BeforeEach(func() {
		reg = plugin.NewRegistry()
		dispatcher = external.NewDispatcher(reg)
		ctx = context.Background()
	})

	Describe("End-to-end spawn and dispatch", func() {
		Context("when spawning and initializing a plugin", func() {
			It("registers plugin and allows dispatch", func() {
				var dispatchCalls []string

				p := &mockHookPlugin{
					name:    "spawn-test",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
							dispatchCalls = append(dispatchCalls, "spawned-plugin")
							return nil
						}),
					},
				}

				Expect(reg.Register(p)).To(Succeed())

				err := dispatcher.Dispatch(ctx, plugin.ChatParams, &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())
				Expect(dispatchCalls).To(Equal([]string{"spawned-plugin"}))
			})
		})

		Context("when initializing multiple plugins", func() {
			It("registers plugins in order", func() {
				p1 := &mockHookPlugin{
					name:    "first-plugin",
					version: "1.0.0",
					hooks:   make(map[plugin.HookType]interface{}),
				}
				p2 := &mockHookPlugin{
					name:    "second-plugin",
					version: "1.0.0",
					hooks:   make(map[plugin.HookType]interface{}),
				}

				Expect(reg.Register(p1)).To(Succeed())
				Expect(reg.Register(p2)).To(Succeed())

				names := reg.Names()
				Expect(names).To(Equal([]string{"first-plugin", "second-plugin"}))
			})
		})
	})

	Describe("Hook dispatch with EventBus integration", func() {
		Context("when dispatching events to multiple hook types", func() {
			It("calls all event hooks with same event payload", func() {
				var eventCalls []string

				p := &mockHookPlugin{
					name:    "event-bus-test",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.EventType: plugin.EventHook(func(_ context.Context, evt plugin.Event) error {
							eventCalls = append(eventCalls, evt.Type())
							return nil
						}),
					},
				}

				Expect(reg.Register(p)).To(Succeed())

				testEvt := &testEvent{
					typ: "integration.test",
					ts:  time.Now(),
					d:   map[string]interface{}{"source": "integration"},
				}

				err := dispatcher.Dispatch(ctx, plugin.EventType, testEvt)
				Expect(err).NotTo(HaveOccurred())
				Expect(eventCalls).To(Equal([]string{"integration.test"}))
			})

			It("preserves event data across multiple handlers", func() {
				var dataCaptures []map[string]interface{}

				p := &mockHookPlugin{
					name:    "event-data-test",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.EventType: plugin.EventHook(func(_ context.Context, evt plugin.Event) error {
							if m, ok := evt.Data().(map[string]interface{}); ok {
								dataCaptures = append(dataCaptures, m)
							}
							return nil
						}),
					},
				}

				Expect(reg.Register(p)).To(Succeed())

				payload := map[string]interface{}{
					"key":    "value",
					"count":  42,
					"active": true,
				}

				testEvt := &testEvent{
					typ: "data.test",
					ts:  time.Now(),
					d:   payload,
				}

				err := dispatcher.Dispatch(ctx, plugin.EventType, testEvt)
				Expect(err).NotTo(HaveOccurred())
				Expect(dataCaptures).To(HaveLen(1))
				Expect(dataCaptures[0]).To(Equal(payload))
			})
		})

		Context("when multiple plugins subscribe to same event", func() {
			It("calls all subscribers in registration order", func() {
				var callOrder []string

				for i := 1; i <= 3; i++ {
					idx := i
					p := &mockHookPlugin{
						name:    "subscriber-" + string(rune('0'+idx)),
						version: "1.0.0",
						hooks: map[plugin.HookType]interface{}{
							plugin.EventType: plugin.EventHook(func(_ context.Context, _ plugin.Event) error {
								callOrder = append(callOrder, "subscriber-"+string(rune('0'+idx)))
								return nil
							}),
						},
					}
					Expect(reg.Register(p)).To(Succeed())
				}

				err := dispatcher.Dispatch(ctx, plugin.EventType, &testEvent{typ: "test"})
				Expect(err).NotTo(HaveOccurred())
				Expect(callOrder).To(Equal([]string{
					"subscriber-1",
					"subscriber-2",
					"subscriber-3",
				}))
			})
		})
	})

	Describe("Multi-plugin concurrent dispatch", func() {
		Context("when dispatching concurrently to multiple plugins", func() {
			It("handles concurrent dispatch without data corruption", func() {
				var (
					dispatchCount int32
					mu            sync.Mutex
					hookCalls     map[string]int
				)
				hookCalls = make(map[string]int)

				for i := 1; i <= 5; i++ {
					idx := i
					p := &mockHookPlugin{
						name:    "concurrent-plugin-" + string(rune('0'+idx)),
						version: "1.0.0",
						hooks: map[plugin.HookType]interface{}{
							plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
								atomic.AddInt32(&dispatchCount, 1)
								mu.Lock()
								hookCalls["plugin-"+string(rune('0'+idx))]++
								mu.Unlock()
								return nil
							}),
						},
					}
					Expect(reg.Register(p)).To(Succeed())
				}

				var wg sync.WaitGroup
				for range 20 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_ = dispatcher.Dispatch(ctx, plugin.ChatParams, &provider.ChatRequest{})
					}()
				}
				wg.Wait()

				Expect(dispatchCount).To(Equal(int32(100)))
				Expect(hookCalls).To(HaveLen(5))
				for _, count := range hookCalls {
					Expect(count).To(Equal(20))
				}
			})
		})

		Context("when plugins have mixed success and failure", func() {
			It("continues after plugin failure and collects errors", func() {
				var callOrder []string

				success1 := &mockHookPlugin{
					name:    "success-1",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
							callOrder = append(callOrder, "success-1")
							return nil
						}),
					},
				}
				failing := &mockHookPlugin{
					name:    "failing",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
							callOrder = append(callOrder, "failing")
							return errors.New("plugin error")
						}),
					},
				}
				success2 := &mockHookPlugin{
					name:    "success-2",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
							callOrder = append(callOrder, "success-2")
							return nil
						}),
					},
				}

				Expect(reg.Register(success1)).To(Succeed())
				Expect(reg.Register(failing)).To(Succeed())
				Expect(reg.Register(success2)).To(Succeed())

				err := dispatcher.Dispatch(ctx, plugin.ChatParams, &provider.ChatRequest{})
				Expect(err).To(HaveOccurred())
				Expect(callOrder).To(Equal([]string{"success-1", "failing", "success-2"}))
			})
		})
	})

	Describe("Plugin failure isolation", func() {
		Context("when a plugin crashes", func() {
			It("does not affect other plugins", func() {
				var calls []string

				panicking := &mockHookPlugin{
					name:    "crasher",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
							calls = append(calls, "crasher")
							panic("plugin crash")
						}),
					},
				}
				normal := &mockHookPlugin{
					name:    "stable",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
							calls = append(calls, "stable")
							return nil
						}),
					},
				}

				Expect(reg.Register(panicking)).To(Succeed())
				Expect(reg.Register(normal)).To(Succeed())

				err := dispatcher.Dispatch(ctx, plugin.ChatParams, &provider.ChatRequest{})
				Expect(err).To(HaveOccurred())
				Expect(calls).To(Equal([]string{"crasher", "stable"}))
			})
		})

		Context("when a plugin returns an error", func() {
			It("other plugins continue and error is collected", func() {
				var calls []string

				failing := &mockHookPlugin{
					name:    "error-thrower",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.EventType: plugin.EventHook(func(_ context.Context, _ plugin.Event) error {
							calls = append(calls, "error-thrower")
							return errors.New("event processing error")
						}),
					},
				}
				succeeding := &mockHookPlugin{
					name:    "event-handler",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.EventType: plugin.EventHook(func(_ context.Context, _ plugin.Event) error {
							calls = append(calls, "event-handler")
							return nil
						}),
					},
				}

				Expect(reg.Register(failing)).To(Succeed())
				Expect(reg.Register(succeeding)).To(Succeed())

				err := dispatcher.Dispatch(ctx, plugin.EventType, &testEvent{})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("error-thrower"))
				Expect(calls).To(Equal([]string{"error-thrower", "event-handler"}))
			})
		})
	})

	Describe("Hook payload type validation across all hook types", func() {
		Context("when dispatching with correct payload types", func() {
			It("successfully dispatches ChatParams hooks with *provider.ChatRequest", func() {
				var called bool

				p := &mockHookPlugin{
					name:    "chat-handler",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, req *provider.ChatRequest) error {
							called = true
							Expect(req).NotTo(BeNil())
							return nil
						}),
					},
				}

				Expect(reg.Register(p)).To(Succeed())

				err := dispatcher.Dispatch(ctx, plugin.ChatParams, &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())
				Expect(called).To(BeTrue())
			})

			It("successfully dispatches EventType hooks with plugin.Event", func() {
				var called bool

				p := &mockHookPlugin{
					name:    "event-handler",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.EventType: plugin.EventHook(func(_ context.Context, evt plugin.Event) error {
							called = true
							Expect(evt).NotTo(BeNil())
							return nil
						}),
					},
				}

				Expect(reg.Register(p)).To(Succeed())

				testEvt := &testEvent{
					typ: "test.event",
					ts:  time.Now(),
					d:   "data",
				}
				err := dispatcher.Dispatch(ctx, plugin.EventType, testEvt)
				Expect(err).NotTo(HaveOccurred())
				Expect(called).To(BeTrue())
			})

			It("successfully dispatches ToolExecBefore hooks with *ToolExecArgs", func() {
				var called bool

				p := &mockHookPlugin{
					name:    "tool-handler",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ToolExecBefore: plugin.ToolExecHook(func(_ context.Context, name string, args map[string]any) error {
							called = true
							Expect(name).To(Equal("bash"))
							Expect(args).To(HaveKeyWithValue("cmd", "echo test"))
							return nil
						}),
					},
				}

				Expect(reg.Register(p)).To(Succeed())

				toolArgs := &external.ToolExecArgs{
					Name: "bash",
					Args: map[string]any{"cmd": "echo test"},
				}
				err := dispatcher.Dispatch(ctx, plugin.ToolExecBefore, toolArgs)
				Expect(err).NotTo(HaveOccurred())
				Expect(called).To(BeTrue())
			})

			It("successfully dispatches ToolExecAfter hooks with *ToolExecArgs", func() {
				var called bool

				p := &mockHookPlugin{
					name:    "tool-after",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ToolExecAfter: plugin.ToolExecHook(func(_ context.Context, name string, args map[string]any) error {
							called = true
							Expect(name).To(Equal("python"))
							return nil
						}),
					},
				}

				Expect(reg.Register(p)).To(Succeed())

				toolArgs := &external.ToolExecArgs{
					Name: "python",
					Args: map[string]any{},
				}
				err := dispatcher.Dispatch(ctx, plugin.ToolExecAfter, toolArgs)
				Expect(err).NotTo(HaveOccurred())
				Expect(called).To(BeTrue())
			})
		})

		Context("when dispatching with incorrect payload types", func() {
			It("returns error for ChatParams hook with wrong payload", func() {
				p := &mockHookPlugin{
					name:    "chat-plugin",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
							return nil
						}),
					},
				}

				Expect(reg.Register(p)).To(Succeed())

				err := dispatcher.Dispatch(ctx, plugin.ChatParams, "invalid-payload")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid payload"))
				Expect(err.Error()).To(ContainSubstring("chat.params"))
			})

			It("returns error for EventType hook with wrong payload", func() {
				p := &mockHookPlugin{
					name:    "event-plugin",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.EventType: plugin.EventHook(func(_ context.Context, _ plugin.Event) error {
							return nil
						}),
					},
				}

				Expect(reg.Register(p)).To(Succeed())

				err := dispatcher.Dispatch(ctx, plugin.EventType, 123)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid payload"))
				Expect(err.Error()).To(ContainSubstring("event"))
			})

			It("returns error for ToolExecBefore hook with wrong payload", func() {
				p := &mockHookPlugin{
					name:    "tool-plugin",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ToolExecBefore: plugin.ToolExecHook(func(_ context.Context, _ string, _ map[string]any) error {
							return nil
						}),
					},
				}

				Expect(reg.Register(p)).To(Succeed())

				err := dispatcher.Dispatch(ctx, plugin.ToolExecBefore, map[string]string{"key": "value"})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid payload"))
				Expect(err.Error()).To(ContainSubstring("tool.execute.before"))
			})
		})
	})
})
