package tool_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/toolset"
)

var _ = Describe("NewDefaultRegistry", func() {
	var registry *tool.Registry

	BeforeEach(func() {
		registry = toolset.NewDefaultRegistry()
	})

	It("returns a non-nil registry", func() {
		Expect(registry).NotTo(BeNil())
	})

	It("returns exactly 3 tools", func() {
		Expect(registry.List()).To(HaveLen(3))
	})

	It("can retrieve bash tool", func() {
		t, err := registry.Get("bash")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Name()).To(Equal("bash"))
	})

	It("can retrieve file tool", func() {
		t, err := registry.Get("file")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Name()).To(Equal("file"))
	})

	It("can retrieve web tool", func() {
		t, err := registry.Get("web")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Name()).To(Equal("web"))
	})

	It("allows bash tool execution", func() {
		Expect(registry.CheckPermission("bash")).To(Equal(tool.Allow))
	})
})
