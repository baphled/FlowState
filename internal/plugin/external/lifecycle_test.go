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

	Describe("externalPlugin.Hooks", func() {
		It("returns hooks matching the manifest's declared hooks", func() {
			manifests := []*manifest.Manifest{
				{
					Name:    "test-plugin",
					Command: "sh",
					Hooks:   []string{"event", "tool.execute.before", "tool.execute.after"},
				},
			}
			Expect(mgr.Start(ctx, manifests)).To(Succeed())

			plugins := reg.List()
			Expect(plugins).To(HaveLen(1))

			ext, ok := plugins[0].(*external.ProcessPlugin)
			Expect(ok).To(BeTrue())

			hooks := ext.Hooks()
			Expect(hooks).To(HaveLen(3))
			Expect(hooks).To(HaveKey(plugin.EventType))
			Expect(hooks).To(HaveKey(plugin.ToolExecBefore))
			Expect(hooks).To(HaveKey(plugin.ToolExecAfter))
		})

		It("returns empty map when manifest declares no hooks", func() {
			manifests := []*manifest.Manifest{
				{
					Name:    "no-hooks-plugin",
					Command: "sh",
					Hooks:   []string{},
				},
			}
			Expect(mgr.Start(ctx, manifests)).To(Succeed())

			plugins := reg.List()
			Expect(plugins).To(HaveLen(1))

			ext, ok := plugins[0].(*external.ProcessPlugin)
			Expect(ok).To(BeTrue())

			hooks := ext.Hooks()
			Expect(hooks).To(BeEmpty())
		})
	})
})
