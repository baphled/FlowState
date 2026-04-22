package toolset_test

import (
	"reflect"
	"sort"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool/toolset"
)

var _ = Describe("NewDefaultRegistry", func() {
	const apiKey = "test-api-key"

	It("returns a non-nil registry", func() {
		registry := toolset.NewDefaultRegistry(apiKey, "")
		Expect(registry).NotTo(BeNil())
	})

	DescribeTable("contains a registered tool",
		func(name string) {
			registry := toolset.NewDefaultRegistry(apiKey, "")
			t, err := registry.Get(name)
			Expect(err).NotTo(HaveOccurred())
			Expect(t).NotTo(BeNil())
		},
		Entry("bash", "bash"),
		Entry("batch", "batch"),
		Entry("read", "read"),
		Entry("write", "write"),
		Entry("edit", "edit"),
		Entry("multiedit", "multiedit"),
		Entry("question", "question"),
		Entry("plan_enter", "plan_enter"),
		Entry("plan_exit", "plan_exit"),
		Entry("plan_list", "plan_list"),
		Entry("plan_read", "plan_read"),
		Entry("invalid", "invalid"),
		Entry("apply_patch", "apply_patch"),
		Entry("web", "web"),
		Entry("websearch", "websearch"),
		Entry("grep", "grep"),
		Entry("ls", "ls"),
	)

	It("registers exactly the expected tools", func() {
		registry := toolset.NewDefaultRegistry(apiKey, "")
		names := make([]string, 0, len(registry.List()))
		for _, registered := range registry.List() {
			names = append(names, registered.Name())
		}
		sort.Strings(names)
		Expect(names).To(Equal([]string{"apply_patch", "bash", "batch", "edit", "grep", "invalid", "ls", "multiedit", "plan_enter", "plan_exit", "plan_list", "plan_read", "question", "read", "web", "websearch", "write"}))
	})

	It("passes the configured API key to websearch", func() {
		registry := toolset.NewDefaultRegistry(apiKey, "")
		registered, err := registry.Get("websearch")
		Expect(err).NotTo(HaveOccurred())

		value := reflect.ValueOf(registered)
		Expect(value.Kind()).To(Equal(reflect.Ptr))
		Expect(value.Elem().Type().PkgPath()).To(Equal("github.com/baphled/flowstate/internal/tool/websearch"))

		field := value.Elem().FieldByName("apiKey")
		Expect(field.IsValid()).To(BeTrue())
		Expect(field.String()).To(Equal(apiKey))
	})
})
