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
		registry = toolset.NewDefaultRegistry("test-key")
	})

	It("returns a non-nil registry", func() {
		Expect(registry).NotTo(BeNil())
	})

	It("returns exactly 15 tools", func() {
		Expect(registry.List()).To(HaveLen(15))
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

	It("can retrieve websearch tool", func() {
		t, err := registry.Get("websearch")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Name()).To(Equal("websearch"))
	})

	It("can retrieve edit tool", func() {
		t, err := registry.Get("edit")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Name()).To(Equal("edit"))
	})

	It("can retrieve multiedit tool", func() {
		t, err := registry.Get("multiedit")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Name()).To(Equal("multiedit"))
	})

	It("can retrieve question tool", func() {
		t, err := registry.Get("question")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Name()).To(Equal("question"))
	})

	It("can retrieve plan enter tool", func() {
		t, err := registry.Get("plan_enter")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Name()).To(Equal("plan_enter"))
	})

	It("can retrieve plan exit tool", func() {
		t, err := registry.Get("plan_exit")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Name()).To(Equal("plan_exit"))
	})

	It("can retrieve invalid tool", func() {
		t, err := registry.Get("invalid")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Name()).To(Equal("invalid"))
	})

	It("can retrieve apply_patch tool", func() {
		t, err := registry.Get("apply_patch")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Name()).To(Equal("apply_patch"))
	})

	It("can retrieve grep tool", func() {
		t, err := registry.Get("grep")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Name()).To(Equal("grep"))
	})

	It("can retrieve ls tool", func() {
		t, err := registry.Get("ls")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Name()).To(Equal("ls"))
	})

	It("can retrieve batch tool", func() {
		t, err := registry.Get("batch")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Name()).To(Equal("batch"))
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
