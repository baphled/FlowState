package external_test

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin"
	"github.com/baphled/flowstate/internal/plugin/external"
	"github.com/baphled/flowstate/internal/plugin/manifest"
)

// fakePluginProc is a fake PluginProcess for lifecycle testing.
type fakePluginProc struct {
	doneCh chan struct{}
	once   sync.Once
}

func newFakePluginProc() *fakePluginProc { return &fakePluginProc{doneCh: make(chan struct{})} }

// crash closes doneCh simulating a process exit.
func (f *fakePluginProc) crash() { f.once.Do(func() { close(f.doneCh) }) }

// fakeSpawner implements external.SpawnIface for testing.
type fakeSpawner struct {
	mu        sync.Mutex
	failSpawn map[string]bool
	failStop  map[string]bool
	delayBy   map[string]time.Duration
	procs     map[string]*fakePluginProc
}

func newFakeSpawner() *fakeSpawner {
	return &fakeSpawner{
		failSpawn: make(map[string]bool),
		failStop:  make(map[string]bool),
		delayBy:   make(map[string]time.Duration),
		procs:     make(map[string]*fakePluginProc),
	}
}

func (f *fakeSpawner) FailOn(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failSpawn[name] = true
}

func (f *fakeSpawner) DelayOn(name string, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delayBy[name] = d
}

func (f *fakeSpawner) FailOnStop(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failStop[name] = true
}

func (f *fakeSpawner) Crash(name string) {
	f.mu.Lock()
	proc, ok := f.procs[name]
	f.mu.Unlock()
	if ok {
		proc.crash()
	}
}

// Spawn implements external.SpawnIface.
func (f *fakeSpawner) Spawn(_ context.Context, m *manifest.Manifest) (*external.PluginProcess, error) {
	f.mu.Lock()
	fail := f.failSpawn[m.Name]
	delay := f.delayBy[m.Name]
	f.mu.Unlock()

	if fail {
		return nil, fmt.Errorf("spawn failed for %q", m.Name)
	}
	if delay > 0 {
		time.Sleep(delay)
	}

	proc := newFakePluginProc()
	f.mu.Lock()
	f.procs[m.Name] = proc
	f.mu.Unlock()

	pr1, pw1 := io.Pipe()
	pr2, pw2 := io.Pipe()

	go func() {
		defer pr1.Close()
		defer pw2.Close()
		buf := make([]byte, 4096)
		n, _ := pr1.Read(buf)
		if n > 0 && delay == 0 {
			resp := `{"jsonrpc":"2.0","id":1,"result":{}}` + "\n"
			_, _ = pw2.Write([]byte(resp))
		}
		<-proc.doneCh
	}()

	return external.NewPluginProcess(pr2, pw1, proc.doneCh), nil
}

// StopProcess implements external.SpawnIface.
func (f *fakeSpawner) StopProcess(name string, _ *external.PluginProcess) error {
	f.mu.Lock()
	fail := f.failStop[name]
	f.mu.Unlock()
	if fail {
		return fmt.Errorf("stop failed for %q", name)
	}
	f.Crash(name)
	return nil
}

var _ = Describe("LifecycleManager", func() {
	var (
		ctx     context.Context
		mgr     *external.LifecycleManager
		reg     *plugin.Registry
		spawner *fakeSpawner
	)

	BeforeEach(func() {
		ctx = context.Background()
		reg = plugin.NewRegistry()
		spawner = newFakeSpawner()
		mgr = external.NewLifecycleManager(spawner, reg)
	})

	Describe("Start", func() {
		It("registers all plugins in the registry on successful start", func() {
			manifests := []*manifest.Manifest{
				{Name: "pluginA", Command: "sh"},
				{Name: "pluginB", Command: "sh"},
			}
			Expect(mgr.Start(ctx, manifests)).To(Succeed())
			Expect(reg.Names()).To(ConsistOf("pluginA", "pluginB"))
		})

		It("returns error and does not register plugin if spawn fails", func() {
			spawner.FailOn("pluginB")
			manifests := []*manifest.Manifest{
				{Name: "pluginA", Command: "sh"},
				{Name: "pluginB", Command: "sh"},
			}
			err := mgr.Start(ctx, manifests)
			Expect(err).To(HaveOccurred())
			Expect(reg.Names()).To(ContainElement("pluginA"))
			Expect(reg.Names()).NotTo(ContainElement("pluginB"))
		})

		It("removes plugin from registry if it crashes after start", func() {
			manifests := []*manifest.Manifest{{Name: "pluginA", Command: "sh"}}
			Expect(mgr.Start(ctx, manifests)).To(Succeed())
			spawner.Crash("pluginA")
			Eventually(func() []string {
				return reg.Names()
			}, "2s").ShouldNot(ContainElement("pluginA"))
		})

		It("aborts and returns error if plugin init times out", func() {
			spawner.DelayOn("pluginA", 3*time.Second)
			manifests := []*manifest.Manifest{{Name: "pluginA", Command: "sh", Timeout: 1}}
			err := mgr.Start(ctx, manifests)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("timeout"))
			Expect(reg.Names()).NotTo(ContainElement("pluginA"))
		})
	})

	Describe("Stop", func() {
		BeforeEach(func() {
			manifests := []*manifest.Manifest{
				{Name: "pluginA", Command: "sh"},
				{Name: "pluginB", Command: "sh"},
			}
			Expect(mgr.Start(ctx, manifests)).To(Succeed())
		})

		It("shuts down all plugins and clears registry", func() {
			Expect(mgr.Stop(ctx)).To(Succeed())
			Expect(reg.Names()).To(BeEmpty())
		})

		It("returns error if any plugin fails to stop but continues shutdown", func() {
			spawner.FailOnStop("pluginB")
			err := mgr.Stop(ctx)
			Expect(err).To(HaveOccurred())
			Expect(reg.Names()).To(BeEmpty())
		})
	})

	It("does not auto-restart crashed plugins", func() {
		manifests := []*manifest.Manifest{{Name: "pluginA", Command: "sh"}}
		Expect(mgr.Start(ctx, manifests)).To(Succeed())
		spawner.Crash("pluginA")
		Eventually(func() []string {
			return reg.Names()
		}, "2s").ShouldNot(ContainElement("pluginA"))
	})

	Describe("externalPlugin methods", func() {
		It("returns correct Name() for externalPlugin", func() {
			manifests := []*manifest.Manifest{{Name: "test-plugin", Command: "sh"}}
			Expect(mgr.Start(ctx, manifests)).To(Succeed())
			plugins := reg.List()
			Expect(plugins).To(HaveLen(1))
			Expect(plugins[0].Name()).To(Equal("test-plugin"))
		})

		It("returns correct Version() for externalPlugin", func() {
			manifests := []*manifest.Manifest{{Name: "test-plugin", Version: "1.2.3", Command: "sh"}}
			Expect(mgr.Start(ctx, manifests)).To(Succeed())
			plugins := reg.List()
			Expect(plugins).To(HaveLen(1))
			Expect(plugins[0].Version()).To(Equal("1.2.3"))
		})

		It("Init() returns nil for externalPlugin", func() {
			manifests := []*manifest.Manifest{{Name: "test-plugin", Command: "sh"}}
			Expect(mgr.Start(ctx, manifests)).To(Succeed())
			plugins := reg.List()
			Expect(plugins).To(HaveLen(1))
			Expect(plugins[0].Init()).To(Succeed())
		})

		It("Shutdown() returns nil for externalPlugin", func() {
			manifests := []*manifest.Manifest{{Name: "test-plugin", Command: "sh"}}
			Expect(mgr.Start(ctx, manifests)).To(Succeed())
			plugins := reg.List()
			Expect(plugins).To(HaveLen(1))

			hp, ok := plugins[0].(interface{ Shutdown() error })
			Expect(ok).To(BeTrue())
			Expect(hp.Shutdown()).To(Succeed())
		})

		It("Hooks() returns map of registered hook types", func() {
			manifests := []*manifest.Manifest{
				{
					Name:    "test-plugin",
					Command: "sh",
					Hooks:   []string{"chat.params", "event"},
				},
			}
			Expect(mgr.Start(ctx, manifests)).To(Succeed())
			plugins := reg.List()
			Expect(plugins).To(HaveLen(1))

			hp, ok := plugins[0].(interface {
				Hooks() map[plugin.HookType]interface{}
			})
			Expect(ok).To(BeTrue())

			hooks := hp.Hooks()
			Expect(hooks).To(HaveKey(plugin.ChatParams))
			Expect(hooks).To(HaveKey(plugin.EventType))
			Expect(hooks).NotTo(HaveKey(plugin.ToolExecBefore))
		})

		It("Hooks() returns empty map when no hooks registered", func() {
			manifests := []*manifest.Manifest{
				{
					Name:    "test-plugin",
					Command: "sh",
					Hooks:   []string{},
				},
			}
			Expect(mgr.Start(ctx, manifests)).To(Succeed())
			plugins := reg.List()
			Expect(plugins).To(HaveLen(1))

			hp, ok := plugins[0].(interface {
				Hooks() map[plugin.HookType]interface{}
			})
			Expect(ok).To(BeTrue())

			hooks := hp.Hooks()
			Expect(hooks).To(BeEmpty())
		})
	})

	Describe("convertManifestHooks", func() {
		It("converts known hook names to HookType constants", func() {
			hookNames := []string{"chat.params", "event", "tool.execute.before", "tool.execute.after"}
			result := external.ConvertManifestHooks(hookNames)
			Expect(result).To(ConsistOf(
				plugin.ChatParams,
				plugin.EventType,
				plugin.ToolExecBefore,
				plugin.ToolExecAfter,
			))
		})

		It("skips unknown hook names", func() {
			hookNames := []string{"chat.params", "unknown.hook", "event"}
			result := external.ConvertManifestHooks(hookNames)
			Expect(result).To(ConsistOf(
				plugin.ChatParams,
				plugin.EventType,
			))
			Expect(result).NotTo(ContainElement("unknown.hook"))
		})

		It("returns empty slice for empty input", func() {
			result := external.ConvertManifestHooks([]string{})
			Expect(result).To(BeEmpty())
		})

		It("handles only unknown hook names", func() {
			hookNames := []string{"invalid.hook", "another.invalid"}
			result := external.ConvertManifestHooks(hookNames)
			Expect(result).To(BeEmpty())
		})

		It("preserves order of known hooks", func() {
			hookNames := []string{"tool.execute.after", "chat.params", "event"}
			result := external.ConvertManifestHooks(hookNames)
			Expect(result).To(Equal([]plugin.HookType{
				plugin.ToolExecAfter,
				plugin.ChatParams,
				plugin.EventType,
			}))
		})

		It("handles duplicate hook names", func() {
			hookNames := []string{"chat.params", "chat.params", "event"}
			result := external.ConvertManifestHooks(hookNames)
			Expect(result).To(ConsistOf(
				plugin.ChatParams,
				plugin.ChatParams,
				plugin.EventType,
			))
		})
	})

	Describe("LifecycleManager — Concurrent Stop", func() {
		It("safely handles concurrent Stop() calls", func() {
			manifests := []*manifest.Manifest{
				{Name: "pluginA", Command: "sh"},
				{Name: "pluginB", Command: "sh"},
			}
			Expect(mgr.Start(ctx, manifests)).To(Succeed())
			Expect(reg.Names()).To(ConsistOf("pluginA", "pluginB"))

			var wg sync.WaitGroup
			errors := make([]error, 5)

			for i := range 5 {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					errors[idx] = mgr.Stop(ctx)
				}(i)
			}
			wg.Wait()

			for _, err := range errors {
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(reg.Names()).To(BeEmpty())
		})
	})

	Describe("LifecycleManager — Crash recovery", func() {
		It("removes plugin from registry when process crashes", func() {
			manifests := []*manifest.Manifest{{Name: "volatile", Command: "sh"}}
			Expect(mgr.Start(ctx, manifests)).To(Succeed())
			Expect(reg.Names()).To(ContainElement("volatile"))

			spawner.Crash("volatile")

			Eventually(func() []string {
				return reg.Names()
			}, "2s").Should(BeEmpty())
		})

		It("continues operating when one plugin crashes", func() {
			manifests := []*manifest.Manifest{
				{Name: "unstable", Command: "sh"},
				{Name: "stable", Command: "sh"},
			}
			Expect(mgr.Start(ctx, manifests)).To(Succeed())
			Expect(reg.Names()).To(ConsistOf("unstable", "stable"))

			spawner.Crash("unstable")

			Eventually(func() []string {
				return reg.Names()
			}, "2s").Should(ContainElement("stable"))
			Eventually(func() []string {
				return reg.Names()
			}, "2s").ShouldNot(ContainElement("unstable"))
		})

		It("handles rapid crash and restart sequence", func() {
			manifests := []*manifest.Manifest{
				{Name: "crashy", Command: "sh"},
			}
			Expect(mgr.Start(ctx, manifests)).To(Succeed())

			spawner.Crash("crashy")
			Eventually(func() []string {
				return reg.Names()
			}, "2s").Should(BeEmpty())
		})
	})

	Describe("LifecycleManager — Kill timeout scenarios", func() {
		It("handles timeout during plugin initialization", func() {
			spawner.DelayOn("timeout-plugin", 3*time.Second)
			manifests := []*manifest.Manifest{
				{Name: "timeout-plugin", Command: "sh", Timeout: 1},
			}
			err := mgr.Start(ctx, manifests)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("timeout"))
			Expect(reg.Names()).NotTo(ContainElement("timeout-plugin"))
		})

		It("continues with remaining plugins after one times out", func() {
			spawner.DelayOn("timeout-plugin", 3*time.Second)
			manifests := []*manifest.Manifest{
				{Name: "good-plugin", Command: "sh"},
				{Name: "timeout-plugin", Command: "sh", Timeout: 1},
			}
			err := mgr.Start(ctx, manifests)
			Expect(err).To(HaveOccurred())
			Expect(reg.Names()).To(ContainElement("good-plugin"))
			Expect(reg.Names()).NotTo(ContainElement("timeout-plugin"))
		})
	})
})
