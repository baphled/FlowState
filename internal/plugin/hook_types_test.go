package plugin

import (
	"reflect"
	"time"

	"github.com/baphled/flowstate/internal/provider"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type sampleEvent struct{}

func (sampleEvent) Type() string { return "sample" }

func (sampleEvent) Timestamp() time.Time { return time.Unix(0, 0) }

func (sampleEvent) Data() any { return nil }

var _ = Describe("Hook type contracts", func() {
	It("defines HookType as a string-based enum with the expected values", func() {
		var hookType HookType

		Expect(reflect.TypeOf(hookType).Kind()).To(Equal(reflect.String))
		Expect(ChatParams).To(Equal(HookType("chat.params")))
		Expect(EventType).To(Equal(HookType("event")))
		Expect(ToolExecBefore).To(Equal(HookType("tool.execute.before")))
		Expect(ToolExecAfter).To(Equal(HookType("tool.execute.after")))
	})

	It("defines the Event interface contract", func() {
		var event Event = sampleEvent{}
		Expect(event.Type()).To(Equal("sample"))
		Expect(event.Timestamp()).To(Equal(time.Unix(0, 0)))
		Expect(event.Data()).To(BeNil())
	})

	It("exposes the expected hook handler function types", func() {
		chatHookType := reflect.TypeOf(ChatParamsHook(nil))
		eventHookType := reflect.TypeOf(EventHook(nil))
		toolHookType := reflect.TypeOf(ToolExecHook(nil))

		Expect(chatHookType.Kind()).To(Equal(reflect.Func))
		Expect(eventHookType.Kind()).To(Equal(reflect.Func))
		Expect(toolHookType.Kind()).To(Equal(reflect.Func))
		Expect(chatHookType.In(1)).To(Equal(reflect.TypeOf(&provider.ChatRequest{})))
		Expect(eventHookType.In(1)).To(Equal(reflect.TypeOf((*Event)(nil)).Elem()))
		Expect(toolHookType.In(1).Kind()).To(Equal(reflect.String))
		Expect(toolHookType.In(2).Kind()).To(Equal(reflect.Map))
		Expect(toolHookType.In(2).Key().Kind()).To(Equal(reflect.String))
		Expect(toolHookType.In(2).Elem().Kind()).To(Equal(reflect.Interface))
	})
})
