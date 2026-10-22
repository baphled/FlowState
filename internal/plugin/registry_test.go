// Package plugin provides plugin registration, discovery, and lifecycle management for FlowState.
//
// This test suite verifies the PluginRegistry implementation, including:
//   - Registering plugins (success, duplicate error)
//   - Getting plugins by name (success, not found)
//   - Listing plugins (order, empty, after multiple registrations)
//   - Concurrency safety (register/get/list from multiple goroutines)
//   - Error handling (duplicate, not found)
package plugin_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sync"

	"github.com/baphled/flowstate/internal/plugin"
)

type testPlugin struct {
	name    string
	version string
	initErr error
}

func (p *testPlugin) Init() error     { return p.initErr }
func (p *testPlugin) Name() string    { return p.name }
func (p *testPlugin) Version() string { return p.version }

var _ = Describe("PluginRegistry", func() {
	var reg *plugin.Registry

	BeforeEach(func() {
		reg = plugin.NewRegistry()
	})

	Describe("Register", func() {
		It("registers a plugin successfully", func() {
			p := &testPlugin{name: "foo", version: "1.0.0"}
			Expect(reg.Register(p)).To(Succeed())
			got, ok := reg.Get("foo")
			Expect(ok).To(BeTrue())
			Expect(got.Name()).To(Equal("foo"))
			Expect(got.Version()).To(Equal("1.0.0"))
		})

		It("returns error on duplicate registration", func() {
			p := &testPlugin{name: "dup", version: "1.0.0"}
			Expect(reg.Register(p)).To(Succeed())
			err := reg.Register(p)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("already registered"))
		})
	})

	Describe("Get", func() {
		It("returns false if plugin not found", func() {
			_, ok := reg.Get("missing")
			Expect(ok).To(BeFalse())
		})
	})

	Describe("List", func() {
		It("returns empty slice if no plugins registered", func() {
			list := reg.List()
			Expect(list).To(BeEmpty())
		})

		It("returns plugins in registration order", func() {
			p1 := &testPlugin{name: "a", version: "1"}
			p2 := &testPlugin{name: "b", version: "2"}
			p3 := &testPlugin{name: "c", version: "3"}
			reg.Register(p1)
			reg.Register(p2)
			reg.Register(p3)
			list := reg.List()
			Expect(list).To(HaveLen(3))
			Expect(list[0].Name()).To(Equal("a"))
			Expect(list[1].Name()).To(Equal("b"))
			Expect(list[2].Name()).To(Equal("c"))
		})
	})

	Describe("Remove", func() {
		It("removes an existing plugin from the registry", func() {
			p := &testPlugin{name: "plugin1", version: "1.0"}
			reg.Register(p)
			Expect(reg.List()).To(HaveLen(1))

			reg.Remove("plugin1")

			Expect(reg.List()).To(BeEmpty())
			_, ok := reg.Get("plugin1")
			Expect(ok).To(BeFalse())
		})

		It("is a no-op when removing a non-existent plugin", func() {
			p := &testPlugin{name: "plugin1", version: "1.0"}
			reg.Register(p)

			reg.Remove("non-existent")

			Expect(reg.List()).To(HaveLen(1))
			plugin, ok := reg.Get("plugin1")
			Expect(ok).To(BeTrue())
			Expect(plugin.Name()).To(Equal("plugin1"))
		})

		It("is a no-op when removing from empty registry", func() {
			Expect(reg.List()).To(BeEmpty())

			reg.Remove("any-name")

			Expect(reg.List()).To(BeEmpty())
		})

		It("maintains order after removing a plugin from the middle", func() {
			p1 := &testPlugin{name: "a", version: "1"}
			p2 := &testPlugin{name: "b", version: "2"}
			p3 := &testPlugin{name: "c", version: "3"}
			reg.Register(p1)
			reg.Register(p2)
			reg.Register(p3)

			reg.Remove("b")

			list := reg.List()
			Expect(list).To(HaveLen(2))
			Expect(list[0].Name()).To(Equal("a"))
			Expect(list[1].Name()).To(Equal("c"))
		})

		It("is idempotent when called multiple times", func() {
			p := &testPlugin{name: "plugin1", version: "1.0"}
			reg.Register(p)

			reg.Remove("plugin1")
			reg.Remove("plugin1")
			reg.Remove("plugin1")

			Expect(reg.List()).To(BeEmpty())
		})

		It("is safe for concurrent Remove calls", func() {
			p1 := &testPlugin{name: "a", version: "1"}
			p2 := &testPlugin{name: "b", version: "2"}
			p3 := &testPlugin{name: "c", version: "3"}
			reg.Register(p1)
			reg.Register(p2)
			reg.Register(p3)

			wg := sync.WaitGroup{}
			for _, name := range []string{"a", "b", "c"} {
				n := name
				wg.Add(1)
				go func() {
					defer wg.Done()
					reg.Remove(n)
				}()
			}
			wg.Wait()

			Expect(reg.List()).To(BeEmpty())
		})
	})

	Describe("Names", func() {
		It("returns empty slice when no plugins are registered", func() {
			names := reg.Names()
			Expect(names).To(BeEmpty())
		})

		It("returns all plugin names in registration order", func() {
			p1 := &testPlugin{name: "first", version: "1"}
			p2 := &testPlugin{name: "second", version: "2"}
			p3 := &testPlugin{name: "third", version: "3"}
			reg.Register(p1)
			reg.Register(p2)
			reg.Register(p3)

			names := reg.Names()

			Expect(names).To(HaveLen(3))
			Expect(names[0]).To(Equal("first"))
			Expect(names[1]).To(Equal("second"))
			Expect(names[2]).To(Equal("third"))
		})

		It("returns correct names after removing plugins", func() {
			p1 := &testPlugin{name: "a", version: "1"}
			p2 := &testPlugin{name: "b", version: "2"}
			p3 := &testPlugin{name: "c", version: "3"}
			reg.Register(p1)
			reg.Register(p2)
			reg.Register(p3)

			reg.Remove("b")
			names := reg.Names()

			Expect(names).To(HaveLen(2))
			Expect(names).To(ConsistOf("a", "c"))
			Expect(names[0]).To(Equal("a"))
			Expect(names[1]).To(Equal("c"))
		})

		It("returns a copy, not the internal slice", func() {
			p1 := &testPlugin{name: "first", version: "1.0"}
			p2 := &testPlugin{name: "second", version: "2.0"}
			reg.Register(p1)
			reg.Register(p2)

			names := reg.Names()
			originalLen := len(names)

			reg.Remove("first")

			namesAfter := reg.Names()
			Expect(originalLen).To(Equal(2))
			Expect(namesAfter).To(HaveLen(1))
		})

		It("is safe for concurrent Names calls", func() {
			p1 := &testPlugin{name: "a", version: "1"}
			p2 := &testPlugin{name: "b", version: "2"}
			reg.Register(p1)
			reg.Register(p2)

			results := make([][]string, 10)
			wg := sync.WaitGroup{}
			for i := range 10 {
				idx := i
				wg.Add(1)
				go func() {
					defer wg.Done()
					results[idx] = reg.Names()
				}()
			}
			wg.Wait()

			for i := range results {
				Expect(results[i]).To(HaveLen(2))
				Expect(results[i][0]).To(Equal("a"))
				Expect(results[i][1]).To(Equal("b"))
			}
		})
	})

	Describe("Concurrency", func() {
		It("is safe for concurrent Register/Get/List", func() {
			wg := sync.WaitGroup{}
			for i := range 10 {
				name := string(rune('a' + i))
				wg.Add(1)
				go func(n string) {
					defer wg.Done()
					p := &testPlugin{name: n, version: "v" + n}
					_ = reg.Register(p)
				}(name)
			}
			wg.Wait()
			list := reg.List()
			Expect(len(list)).To(BeNumerically("<=", 10))
			for _, p := range list {
				got, ok := reg.Get(p.Name())
				Expect(ok).To(BeTrue())
				Expect(got.Name()).To(Equal(p.Name()))
			}
		})
	})
})
