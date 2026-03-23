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

	It("returns exactly 4 tools", func() {
		Expect(registry.List()).To(HaveLen(4))
	})

	It("can retrieve bash tool", func() {
		t, err := registry.Get("bash")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Name()).To(Equal("bash"))
	})

	It("can retrieve read tool", func() {
		t, err := registry.Get("read")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Name()).To(Equal("read"))
	})

	It("can retrieve write tool", func() {
		t, err := registry.Get("write")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Name()).To(Equal("write"))
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

var _ = Describe("CheckPermission", func() {
	var registry *tool.Registry

	BeforeEach(func() {
		registry = tool.NewRegistry()
	})

	Context("when no permission is configured for a tool", func() {
		It("defaults to Allow", func() {
			Expect(registry.CheckPermission("unknown-tool")).To(Equal(tool.Allow))
		})
	})

	Context("when a tool permission is set to Ask", func() {
		BeforeEach(func() {
			registry.SetPermission("mcp-tool", tool.Ask)
		})

		It("returns Ask for the configured tool", func() {
			Expect(registry.CheckPermission("mcp-tool")).To(Equal(tool.Ask))
		})

		It("returns Allow for unconfigured tools", func() {
			Expect(registry.CheckPermission("other-tool")).To(Equal(tool.Allow))
		})
	})

	Context("when a tool permission is set to Deny", func() {
		BeforeEach(func() {
			registry.SetPermission("dangerous-tool", tool.Deny)
		})

		It("returns Deny for the configured tool", func() {
			Expect(registry.CheckPermission("dangerous-tool")).To(Equal(tool.Deny))
		})
	})

	Context("when a tool permission is set to Allow explicitly", func() {
		BeforeEach(func() {
			registry.SetPermission("safe-tool", tool.Allow)
		})

		It("returns Allow for the configured tool", func() {
			Expect(registry.CheckPermission("safe-tool")).To(Equal(tool.Allow))
		})
	})

	Context("when multiple permissions are configured", func() {
		BeforeEach(func() {
			registry.SetPermission("tool-a", tool.Ask)
			registry.SetPermission("tool-b", tool.Deny)
			registry.SetPermission("tool-c", tool.Allow)
		})

		It("returns the correct permission for each tool", func() {
			Expect(registry.CheckPermission("tool-a")).To(Equal(tool.Ask))
			Expect(registry.CheckPermission("tool-b")).To(Equal(tool.Deny))
			Expect(registry.CheckPermission("tool-c")).To(Equal(tool.Allow))
		})
	})
})
