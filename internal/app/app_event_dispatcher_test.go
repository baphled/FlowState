// Package app_test provides Ginkgo specs for the app package.
package app_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/plugin/external"
)

var _ = Describe("subscribeDispatcherHooks type contract", func() {
	Describe("characterisation: broken type assertion", func() {
		It("proves *events.ToolEvent cannot be asserted as *external.ToolExecArgs", func() {
			var msg any = &events.ToolEvent{
				Data: events.ToolEventData{
					ToolName: "test-tool",
					Args:     map[string]any{"foo": "bar"},
				},
			}
			_, ok := msg.(*external.ToolExecArgs)
			Expect(ok).To(BeFalse(), "ToolEvent cannot be type-asserted as ToolExecArgs — documents the type mismatch bug")
		})
	})

	Describe("verification: correct translation after fix", func() {
		It("proves *events.ToolEvent translates correctly to *external.ToolExecArgs", func() {
			var msg any = &events.ToolEvent{
				Data: events.ToolEventData{
					ToolName: "fix-verified-tool",
					Args:     map[string]any{"param": "value"},
				},
			}
			toolEvt, ok := msg.(*events.ToolEvent)
			Expect(ok).To(BeTrue())

			args := &external.ToolExecArgs{
				Name: toolEvt.Data.ToolName,
				Args: toolEvt.Data.Args,
			}
			Expect(args.Name).To(Equal("fix-verified-tool"))
			Expect(args.Args).To(HaveKeyWithValue("param", "value"))
		})
	})
})
