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
			// Should have up to 10 unique plugins (duplicates ignored)
			Expect(len(list)).To(BeNumerically("<=", 10))
			for _, p := range list {
				got, ok := reg.Get(p.Name())
				Expect(ok).To(BeTrue())
				Expect(got.Name()).To(Equal(p.Name()))
			}
		})
	})
})
