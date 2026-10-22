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

type mockHookPlugin struct {
	name    string
	version string
	hooks   map[plugin.HookType]interface{}
}

func (m *mockHookPlugin) Init() error                            { return nil }
func (m *mockHookPlugin) Name() string                           { return m.name }
func (m *mockHookPlugin) Version() string                        { return m.version }
func (m *mockHookPlugin) Hooks() map[plugin.HookType]interface{} { return m.hooks }

type plainPlugin struct {
	name    string
	version string
}

func (p *plainPlugin) Init() error     { return nil }
func (p *plainPlugin) Name() string    { return p.name }
func (p *plainPlugin) Version() string { return p.version }

type testEvent struct {
	typ string
	ts  time.Time
	d   any
}

func (e *testEvent) Type() string         { return e.typ }
func (e *testEvent) Timestamp() time.Time { return e.ts }
func (e *testEvent) Data() any            { return e.d }

var _ = Describe("Dispatcher", func() {
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

	Describe("Dispatch", func() {
		Context("when plugins have matching hooks", func() {
			It("calls all plugins in registration order", func() {
				var calls []string

				p1 := &mockHookPlugin{
					name:    "plugin-a",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
							calls = append(calls, "plugin-a")
							return nil
						}),
					},
				}
				p2 := &mockHookPlugin{
					name:    "plugin-b",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
							calls = append(calls, "plugin-b")
							return nil
						}),
					},
				}

				Expect(reg.Register(p1)).To(Succeed())
				Expect(reg.Register(p2)).To(Succeed())

				err := dispatcher.Dispatch(ctx, plugin.ChatParams, &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())
				Expect(calls).To(Equal([]string{"plugin-a", "plugin-b"}))
			})
		})

		Context("when a plugin does not implement HookProvider", func() {
			It("skips the plugin without error", func() {
				var calls []string

				plain := &plainPlugin{name: "no-hooks", version: "1.0.0"}
				hooked := &mockHookPlugin{
					name:    "has-hooks",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
							calls = append(calls, "has-hooks")
							return nil
						}),
					},
				}

				Expect(reg.Register(plain)).To(Succeed())
				Expect(reg.Register(hooked)).To(Succeed())

				err := dispatcher.Dispatch(ctx, plugin.ChatParams, &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())
				Expect(calls).To(Equal([]string{"has-hooks"}))
			})
		})

		Context("when a plugin does not have the target hook type", func() {
			It("skips the plugin", func() {
				var calls []string

				p := &mockHookPlugin{
					name:    "event-only",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.EventType: plugin.EventHook(func(_ context.Context, _ plugin.Event) error {
							calls = append(calls, "should-not-be-called")
							return nil
						}),
					},
				}

				Expect(reg.Register(p)).To(Succeed())

				err := dispatcher.Dispatch(ctx, plugin.ChatParams, &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())
				Expect(calls).To(BeEmpty())
			})
		})

		Context("when a plugin hook returns an error", func() {
			It("continues dispatching and returns combined errors", func() {
				var calls []string

				failing := &mockHookPlugin{
					name:    "failing-plugin",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
							calls = append(calls, "failing-plugin")
							return errors.New("hook failed")
						}),
					},
				}
				succeeding := &mockHookPlugin{
					name:    "succeeding-plugin",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
							calls = append(calls, "succeeding-plugin")
							return nil
						}),
					},
				}

				Expect(reg.Register(failing)).To(Succeed())
				Expect(reg.Register(succeeding)).To(Succeed())

				err := dispatcher.Dispatch(ctx, plugin.ChatParams, &provider.ChatRequest{})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failing-plugin"))
				Expect(calls).To(Equal([]string{"failing-plugin", "succeeding-plugin"}))
			})
		})
	})

	Describe("Dispatcher — EventHook", func() {
		Context("when dispatching event hooks", func() {
			It("calls all plugins with matching EventHook", func() {
				var calls []string

				p1 := &mockHookPlugin{
					name:    "event-plugin-1",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.EventType: plugin.EventHook(func(_ context.Context, _ plugin.Event) error {
							calls = append(calls, "event-plugin-1")
							return nil
						}),
					},
				}
				p2 := &mockHookPlugin{
					name:    "event-plugin-2",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.EventType: plugin.EventHook(func(_ context.Context, _ plugin.Event) error {
							calls = append(calls, "event-plugin-2")
							return nil
						}),
					},
				}

				Expect(reg.Register(p1)).To(Succeed())
				Expect(reg.Register(p2)).To(Succeed())

				testEvent := &testEvent{
					typ: "test.event",
					ts:  time.Now(),
					d:   map[string]interface{}{"key": "value"},
				}

				err := dispatcher.Dispatch(ctx, plugin.EventType, testEvent)
				Expect(err).NotTo(HaveOccurred())
				Expect(calls).To(Equal([]string{"event-plugin-1", "event-plugin-2"}))
			})

			It("skips plugins without EventHook", func() {
				var calls []string

				noHook := &mockHookPlugin{
					name:    "no-event",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
							return nil
						}),
					},
				}
				withHook := &mockHookPlugin{
					name:    "with-event",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.EventType: plugin.EventHook(func(_ context.Context, _ plugin.Event) error {
							calls = append(calls, "with-event")
							return nil
						}),
					},
				}

				Expect(reg.Register(noHook)).To(Succeed())
				Expect(reg.Register(withHook)).To(Succeed())

				err := dispatcher.Dispatch(ctx, plugin.EventType, &testEvent{})
				Expect(err).NotTo(HaveOccurred())
				Expect(calls).To(Equal([]string{"with-event"}))
			})

			It("returns error when event hook fails", func() {
				p := &mockHookPlugin{
					name:    "failing-event",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.EventType: plugin.EventHook(func(_ context.Context, _ plugin.Event) error {
							return errors.New("event processing failed")
						}),
					},
				}

				Expect(reg.Register(p)).To(Succeed())

				err := dispatcher.Dispatch(ctx, plugin.EventType, &testEvent{})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("failing-event"))
			})
		})
	})

	Describe("Dispatcher — ToolExecBefore/After", func() {
		Context("when dispatching ToolExecBefore hooks", func() {
			It("calls all plugins with matching ToolExecBefore hook", func() {
				var calls []string

				p1 := &mockHookPlugin{
					name:    "tool-before-1",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ToolExecBefore: plugin.ToolExecHook(func(_ context.Context, name string, _ map[string]any) error {
							calls = append(calls, "tool-before-1:"+name)
							return nil
						}),
					},
				}
				p2 := &mockHookPlugin{
					name:    "tool-before-2",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ToolExecBefore: plugin.ToolExecHook(func(_ context.Context, name string, _ map[string]any) error {
							calls = append(calls, "tool-before-2:"+name)
							return nil
						}),
					},
				}

				Expect(reg.Register(p1)).To(Succeed())
				Expect(reg.Register(p2)).To(Succeed())

				args := &external.ToolExecArgs{
					Name: "bash",
					Args: map[string]any{"cmd": "echo hello"},
				}

				err := dispatcher.Dispatch(ctx, plugin.ToolExecBefore, args)
				Expect(err).NotTo(HaveOccurred())
				Expect(calls).To(Equal([]string{"tool-before-1:bash", "tool-before-2:bash"}))
			})

			It("continues on failure and returns combined errors", func() {
				var calls []string

				p1 := &mockHookPlugin{
					name:    "tool-fail",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ToolExecBefore: plugin.ToolExecHook(func(_ context.Context, _ string, _ map[string]any) error {
							calls = append(calls, "tool-fail")
							return errors.New("tool validation failed")
						}),
					},
				}
				p2 := &mockHookPlugin{
					name:    "tool-ok",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ToolExecBefore: plugin.ToolExecHook(func(_ context.Context, _ string, _ map[string]any) error {
							calls = append(calls, "tool-ok")
							return nil
						}),
					},
				}

				Expect(reg.Register(p1)).To(Succeed())
				Expect(reg.Register(p2)).To(Succeed())

				args := &external.ToolExecArgs{Name: "bash", Args: map[string]any{}}

				err := dispatcher.Dispatch(ctx, plugin.ToolExecBefore, args)
				Expect(err).To(HaveOccurred())
				Expect(calls).To(Equal([]string{"tool-fail", "tool-ok"}))
			})
		})

		Context("when dispatching ToolExecAfter hooks", func() {
			It("calls all plugins with matching ToolExecAfter hook", func() {
				var calls []string

				p1 := &mockHookPlugin{
					name:    "tool-after-1",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ToolExecAfter: plugin.ToolExecHook(func(_ context.Context, name string, _ map[string]any) error {
							calls = append(calls, "tool-after-1:"+name)
							return nil
						}),
					},
				}

				Expect(reg.Register(p1)).To(Succeed())

				args := &external.ToolExecArgs{
					Name: "bash",
					Args: map[string]any{"result": "success"},
				}

				err := dispatcher.Dispatch(ctx, plugin.ToolExecAfter, args)
				Expect(err).NotTo(HaveOccurred())
				Expect(calls).To(Equal([]string{"tool-after-1:bash"}))
			})
		})
	})

	Describe("Dispatcher — Invalid payload types", func() {
		Context("when payload type does not match hook expectation", func() {
			It("returns error for ChatParams hook with wrong payload type", func() {
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

				err := dispatcher.Dispatch(ctx, plugin.ChatParams, "invalid payload")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid payload"))
			})

			It("returns error for EventHook with wrong payload type", func() {
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

				err := dispatcher.Dispatch(ctx, plugin.EventType, "not an event")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid payload"))
			})

			It("returns error for ToolExecBefore hook with wrong payload type", func() {
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

				err := dispatcher.Dispatch(ctx, plugin.ToolExecBefore, map[string]string{"invalid": "type"})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid payload"))
			})

			It("returns error for ToolExecAfter hook with wrong payload type", func() {
				p := &mockHookPlugin{
					name:    "tool-plugin",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ToolExecAfter: plugin.ToolExecHook(func(_ context.Context, _ string, _ map[string]any) error {
							return nil
						}),
					},
				}

				Expect(reg.Register(p)).To(Succeed())

				err := dispatcher.Dispatch(ctx, plugin.ToolExecAfter, 42)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid payload"))
			})
		})
	})

	Describe("Dispatcher — Concurrent dispatch", func() {
		Context("when multiple goroutines dispatch hooks concurrently", func() {
			It("safely dispatches to all plugins without race conditions", func() {
				var (
					counter  int32
					mu       sync.Mutex
					maxCount int32
				)

				for i := range 5 {
					p := &mockHookPlugin{
						name:    "concurrent-" + string(rune('0'+i)),
						version: "1.0.0",
						hooks: map[plugin.HookType]interface{}{
							plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
								atomic.AddInt32(&counter, 1)
								mu.Lock()
								if atomic.LoadInt32(&counter) > maxCount {
									maxCount = atomic.LoadInt32(&counter)
								}
								mu.Unlock()
								return nil
							}),
						},
					}
					Expect(reg.Register(p)).To(Succeed())
				}

				var wg sync.WaitGroup
				for range 10 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_ = dispatcher.Dispatch(ctx, plugin.ChatParams, &provider.ChatRequest{})
					}()
				}
				wg.Wait()

				Expect(counter).To(Equal(int32(50)))
			})

			It("maintains isolation between concurrent dispatches", func() {
				var (
					chatCalls  int32
					eventCalls int32
				)

				p := &mockHookPlugin{
					name:    "multi-hook",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
							atomic.AddInt32(&chatCalls, 1)
							return nil
						}),
						plugin.EventType: plugin.EventHook(func(_ context.Context, _ plugin.Event) error {
							atomic.AddInt32(&eventCalls, 1)
							return nil
						}),
					},
				}

				Expect(reg.Register(p)).To(Succeed())

				var wg sync.WaitGroup
				for range 5 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_ = dispatcher.Dispatch(ctx, plugin.ChatParams, &provider.ChatRequest{})
					}()
					wg.Add(1)
					go func() {
						defer wg.Done()
						_ = dispatcher.Dispatch(ctx, plugin.EventType, &testEvent{})
					}()
				}
				wg.Wait()

				Expect(chatCalls).To(Equal(int32(5)))
				Expect(eventCalls).To(Equal(int32(5)))
			})
		})
	})

	Describe("Dispatcher — Panic recovery", func() {
		Context("when a hook function panics", func() {
			It("recovers from panic and continues dispatching to next plugin", func() {
				var calls []string

				panicking := &mockHookPlugin{
					name:    "panicking-plugin",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
							calls = append(calls, "panicking")
							panic("intentional panic")
						}),
					},
				}
				normal := &mockHookPlugin{
					name:    "normal-plugin",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
							calls = append(calls, "normal")
							return nil
						}),
					},
				}

				Expect(reg.Register(panicking)).To(Succeed())
				Expect(reg.Register(normal)).To(Succeed())

				err := dispatcher.Dispatch(ctx, plugin.ChatParams, &provider.ChatRequest{})
				Expect(err).To(HaveOccurred())
				Expect(calls).To(Equal([]string{"panicking", "normal"}))
			})

			It("recovers from panic in EventHook and continues", func() {
				var calls []string

				panicking := &mockHookPlugin{
					name:    "panicking-event",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.EventType: plugin.EventHook(func(_ context.Context, _ plugin.Event) error {
							calls = append(calls, "panic")
							panic("event panic")
						}),
					},
				}
				succeeding := &mockHookPlugin{
					name:    "succeeding-event",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.EventType: plugin.EventHook(func(_ context.Context, _ plugin.Event) error {
							calls = append(calls, "success")
							return nil
						}),
					},
				}

				Expect(reg.Register(panicking)).To(Succeed())
				Expect(reg.Register(succeeding)).To(Succeed())

				err := dispatcher.Dispatch(ctx, plugin.EventType, &testEvent{})
				Expect(err).To(HaveOccurred())
				Expect(calls).To(Equal([]string{"panic", "success"}))
			})

			It("recovers from panic in ToolExecBefore hook", func() {
				var calls []string

				panicking := &mockHookPlugin{
					name:    "panicking-tool",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ToolExecBefore: plugin.ToolExecHook(func(_ context.Context, _ string, _ map[string]any) error {
							calls = append(calls, "panic")
							panic("tool panic")
						}),
					},
				}
				normal := &mockHookPlugin{
					name:    "normal-tool",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ToolExecBefore: plugin.ToolExecHook(func(_ context.Context, _ string, _ map[string]any) error {
							calls = append(calls, "ok")
							return nil
						}),
					},
				}

				Expect(reg.Register(panicking)).To(Succeed())
				Expect(reg.Register(normal)).To(Succeed())

				args := &external.ToolExecArgs{Name: "bash", Args: map[string]any{}}
				err := dispatcher.Dispatch(ctx, plugin.ToolExecBefore, args)
				Expect(err).To(HaveOccurred())
				Expect(calls).To(Equal([]string{"panic", "ok"}))
			})

			It("recovers from panic when multiple plugins panic concurrently", func() {
				var counter int32

				for i := range 3 {
					idx := i
					p := &mockHookPlugin{
						name:    "panic-" + string(rune('0'+idx)),
						version: "1.0.0",
						hooks: map[plugin.HookType]interface{}{
							plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
								atomic.AddInt32(&counter, 1)
								panic("concurrent panic " + string(rune('0'+idx)))
							}),
						},
					}
					Expect(reg.Register(p)).To(Succeed())
				}

				normal := &mockHookPlugin{
					name:    "normal-concurrent",
					version: "1.0.0",
					hooks: map[plugin.HookType]interface{}{
						plugin.ChatParams: plugin.ChatParamsHook(func(_ context.Context, _ *provider.ChatRequest) error {
							atomic.AddInt32(&counter, 1)
							return nil
						}),
					},
				}
				Expect(reg.Register(normal)).To(Succeed())

				var wg sync.WaitGroup
				for range 5 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_ = dispatcher.Dispatch(ctx, plugin.ChatParams, &provider.ChatRequest{})
					}()
				}
				wg.Wait()

				Expect(counter).To(Equal(int32(20)))
			})
		})
	})
})
