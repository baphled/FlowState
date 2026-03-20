package toolset_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool/toolset"
)

var _ = Describe("NewDefaultRegistry", func() {
	It("returns a non-nil registry", func() {
		registry := toolset.NewDefaultRegistry()
		Expect(registry).NotTo(BeNil())
	})

	It("contains the bash tool", func() {
		registry := toolset.NewDefaultRegistry()
		t, err := registry.Get("bash")
		Expect(err).NotTo(HaveOccurred())
		Expect(t).NotTo(BeNil())
	})

	It("contains the file tool", func() {
		registry := toolset.NewDefaultRegistry()
		t, err := registry.Get("file")
		Expect(err).NotTo(HaveOccurred())
		Expect(t).NotTo(BeNil())
	})

	It("contains the web tool", func() {
		registry := toolset.NewDefaultRegistry()
		t, err := registry.Get("web")
		Expect(err).NotTo(HaveOccurred())
		Expect(t).NotTo(BeNil())
	})
})
