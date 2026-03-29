package external_test

import (
	"context"
	"errors"

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
})
