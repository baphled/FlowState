package plugin_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
)

type builtinTestPlugin struct {
	name    string
	version string
	initErr error
}

func (p *builtinTestPlugin) Init() error     { return p.initErr }
func (p *builtinTestPlugin) Name() string    { return p.name }
func (p *builtinTestPlugin) Version() string { return p.version }

type builtinTestBusStarter struct {
	*builtinTestPlugin
	startErr error
	started  bool
}

func (t *builtinTestBusStarter) Start(bus *eventbus.EventBus) error {
	t.started = true
	return t.startErr
}

func returnNilPlugin() (plugin.Plugin, error) {
	var p plugin.Plugin
	return p, nil
}

var _ = Describe("Builtin", func() {
	var reg *plugin.Registry
	var bus *eventbus.EventBus

	BeforeEach(func() {
		plugin.ResetBuiltins()
		reg = plugin.NewRegistry()
		bus = eventbus.NewEventBus()
	})

	Describe("RegisterBuiltin", func() {
		It("stores factory in global registry", func() {
			var called bool
			plugin.RegisterBuiltin(plugin.Registration{
				Name:             "test",
				Order:            0,
				EnabledByDefault: true,
				Factory: func(deps plugin.Deps) (plugin.Plugin, error) {
					called = true
					return &builtinTestPlugin{name: "test", version: "1.0.0"}, nil
				},
			})

			deps := plugin.Deps{Registry: reg}
			err := plugin.LoadBuiltins(deps)

			Expect(err).To(Succeed())
			Expect(called).To(BeTrue())
			p, ok := reg.Get("test")
			Expect(ok).To(BeTrue())
			Expect(p.Name()).To(Equal("test"))
		})
	})

	Describe("LoadBuiltins", func() {
		It("calls factory and registers plugin", func() {
			plugin.RegisterBuiltin(plugin.Registration{
				Name:             "factory-plugin",
				Order:            0,
				EnabledByDefault: true,
				Factory: func(deps plugin.Deps) (plugin.Plugin, error) {
					return &builtinTestPlugin{name: "factory-plugin", version: "2.0.0"}, nil
				},
			})

			err := plugin.LoadBuiltins(plugin.Deps{Registry: reg})

			Expect(err).To(Succeed())
			p, ok := reg.Get("factory-plugin")
			Expect(ok).To(BeTrue())
			Expect(p.Version()).To(Equal("2.0.0"))
		})

		It("returns error when factory fails", func() {
			expectedErr := errors.New("factory failed")
			plugin.RegisterBuiltin(plugin.Registration{
				Name:             "fail-plugin",
				Order:            0,
				EnabledByDefault: true,
				Factory: func(deps plugin.Deps) (plugin.Plugin, error) {
					return nil, expectedErr
				},
			})

			err := plugin.LoadBuiltins(plugin.Deps{Registry: reg})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("loading builtin plugin"))
		})

		It("returns error when registration fails", func() {
			plugin.RegisterBuiltin(plugin.Registration{
				Name:             "test",
				Order:            0,
				EnabledByDefault: true,
				Factory: func(deps plugin.Deps) (plugin.Plugin, error) {
					return &builtinTestPlugin{name: "test", version: "1.0.0"}, nil
				},
			})
			plugin.RegisterBuiltin(plugin.Registration{
				Name:             "test",
				Order:            1,
				EnabledByDefault: true,
				Factory: func(deps plugin.Deps) (plugin.Plugin, error) {
					return &builtinTestPlugin{name: "test", version: "2.0.0"}, nil
				},
			})

			err := plugin.LoadBuiltins(plugin.Deps{Registry: reg})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("registering builtin plugin"))
		})
	})

	Describe("LoadBuiltins hardening", func() {
		It("returns an error when factory panics", func() {
			plugin.RegisterBuiltin(plugin.Registration{
				Name:             "panic-plugin",
				Order:            0,
				EnabledByDefault: true,
				Factory: func(deps plugin.Deps) (plugin.Plugin, error) {
					panic("boom")
				},
			})

			err := plugin.LoadBuiltins(plugin.Deps{Registry: reg})

			Expect(err).To(MatchError(ContainSubstring("panic")))
		})

		It("returns an error when factory returns nil plugin", func() {
			plugin.RegisterBuiltin(plugin.Registration{
				Name:             "nil-plugin",
				Order:            0,
				EnabledByDefault: true,
				Factory:          func(deps plugin.Deps) (plugin.Plugin, error) { return returnNilPlugin() },
			})

			err := plugin.LoadBuiltins(plugin.Deps{Registry: reg})

			Expect(err).To(MatchError(ContainSubstring("nil plugin")))
		})

		It("returns an error when plugin appears in both Enabled and Disabled", func() {
			plugin.RegisterBuiltin(plugin.Registration{
				Name:             "conflict-plugin",
				Order:            0,
				EnabledByDefault: true,
				Factory: func(deps plugin.Deps) (plugin.Plugin, error) {
					return &builtinTestPlugin{name: "conflict-plugin", version: "1.0.0"}, nil
				},
			})

			err := plugin.LoadBuiltins(plugin.Deps{
				Registry: reg,
				PluginsConfig: plugin.PluginsConf{
					Enabled:  []string{"conflict-plugin"},
					Disabled: []string{"conflict-plugin"},
				},
			})

			Expect(err).To(MatchError(ContainSubstring("conflict")))
		})

		It("is idempotent — second call returns nil and registers plugin exactly once", func() {
			calls := 0
			plugin.RegisterBuiltin(plugin.Registration{
				Name:             "idempotent-plugin",
				Order:            0,
				EnabledByDefault: true,
				Factory: func(deps plugin.Deps) (plugin.Plugin, error) {
					calls++
					return &builtinTestPlugin{name: "idempotent-plugin", version: "1.0.0"}, nil
				},
			})

			err1 := plugin.LoadBuiltins(plugin.Deps{Registry: reg})
			err2 := plugin.LoadBuiltins(plugin.Deps{Registry: reg})

			Expect(err1).NotTo(HaveOccurred())
			Expect(err2).NotTo(HaveOccurred())
			Expect(calls).To(Equal(1))
			Expect(reg.Names()).To(HaveLen(1))
		})
	})

	Describe("StartBusPlugins", func() {
		It("calls Start on BusStarter plugins only", func() {
			regularPlugin := &builtinTestPlugin{name: "regular", version: "1.0.0"}
			busStarterPlugin := &builtinTestBusStarter{
				builtinTestPlugin: &builtinTestPlugin{name: "starter", version: "1.0.0"},
			}

			reg.Register(regularPlugin)
			reg.Register(busStarterPlugin)

			err := plugin.StartBusPlugins(reg, bus)

			Expect(err).To(Succeed())
			Expect(busStarterPlugin.started).To(BeTrue())
		})

		It("returns error when Start fails", func() {
			failingStarter := &builtinTestBusStarter{
				builtinTestPlugin: &builtinTestPlugin{name: "failing", version: "1.0.0"},
				startErr:          errors.New("start failed"),
			}

			reg.Register(failingStarter)

			err := plugin.StartBusPlugins(reg, bus)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("starting plugin"))
		})

		It("succeeds when no BusStarter plugins exist", func() {
			p := &builtinTestPlugin{name: "nop", version: "1.0.0"}
			reg.Register(p)

			err := plugin.StartBusPlugins(reg, bus)

			Expect(err).To(Succeed())
		})
	})
})
