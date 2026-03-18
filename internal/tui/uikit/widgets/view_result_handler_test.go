package widgets_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

var _ = Describe("HandleViewResult", func() {
	Describe("nil result", func() {
		It("returns nil when result is nil", func() {
			cmd := widgets.HandleViewResult(nil, nil, nil)
			Expect(cmd).To(BeNil())
		})
	})

	Describe("ResultNavigate", func() {
		It("calls handler when action exists in actions map", func() {
			handlerCalled := false
			actions := map[string]widgets.ActionHandler{
				"test-action": func(data map[string]interface{}) tea.Cmd {
					handlerCalled = true
					return nil
				},
			}

			result := &widgets.NavigateViewResult{
				ResultData: map[string]interface{}{
					"action": "test-action",
					"value":  "test-value",
				},
			}

			cmd := widgets.HandleViewResult(result, actions, nil)
			Expect(handlerCalled).To(BeTrue())
			Expect(cmd).To(BeNil())
		})

		It("returns nil when action does not exist in actions map", func() {
			actions := map[string]widgets.ActionHandler{
				"other-action": func(data map[string]interface{}) tea.Cmd {
					return nil
				},
			}

			result := &widgets.NavigateViewResult{
				ResultData: map[string]interface{}{
					"action": "unknown-action",
				},
			}

			cmd := widgets.HandleViewResult(result, actions, nil)
			Expect(cmd).To(BeNil())
		})

		It("returns nil when data is not a map", func() {
			actions := map[string]widgets.ActionHandler{
				"test-action": func(data map[string]interface{}) tea.Cmd {
					return nil
				},
			}

			result := &widgets.NavigateViewResult{
				ResultData: "not-a-map",
			}

			cmd := widgets.HandleViewResult(result, actions, nil)
			Expect(cmd).To(BeNil())
		})

		It("returns nil when action field is not a string", func() {
			actions := map[string]widgets.ActionHandler{
				"test-action": func(data map[string]interface{}) tea.Cmd {
					return nil
				},
			}

			result := &widgets.NavigateViewResult{
				ResultData: map[string]interface{}{
					"action": 123,
				},
			}

			cmd := widgets.HandleViewResult(result, actions, nil)
			Expect(cmd).To(BeNil())
		})

		It("passes action data to handler", func() {
			var receivedData map[string]interface{}
			actions := map[string]widgets.ActionHandler{
				"test-action": func(data map[string]interface{}) tea.Cmd {
					receivedData = data
					return nil
				},
			}

			expectedData := map[string]interface{}{
				"action": "test-action",
				"value":  "test-value",
				"id":     42,
			}

			result := &widgets.NavigateViewResult{
				ResultData: expectedData,
			}

			widgets.HandleViewResult(result, actions, nil)
			Expect(receivedData).To(Equal(expectedData))
		})

		It("returns command from handler", func() {
			cmdReturned := false
			actions := map[string]widgets.ActionHandler{
				"test-action": func(data map[string]interface{}) tea.Cmd {
					cmdReturned = true
					return tea.Cmd(func() tea.Msg { return nil })
				},
			}

			result := &widgets.NavigateViewResult{
				ResultData: map[string]interface{}{
					"action": "test-action",
				},
			}

			cmd := widgets.HandleViewResult(result, actions, nil)
			Expect(cmdReturned).To(BeTrue())
			Expect(cmd).NotTo(BeNil())
		})
	})

	Describe("ResultCancel", func() {
		It("calls onCancel callback when provided", func() {
			cancelCalled := false
			onCancel := func() {
				cancelCalled = true
			}

			result := &widgets.CancelViewResult{}
			cmd := widgets.HandleViewResult(result, nil, onCancel)

			Expect(cancelCalled).To(BeTrue())
			Expect(cmd).To(BeNil())
		})

		It("returns nil when onCancel is nil", func() {
			result := &widgets.CancelViewResult{}
			cmd := widgets.HandleViewResult(result, nil, nil)
			Expect(cmd).To(BeNil())
		})

		It("does not call onCancel when result is not ResultCancel", func() {
			cancelCalled := false
			onCancel := func() {
				cancelCalled = true
			}

			result := &widgets.NavigateViewResult{
				ResultData: map[string]interface{}{
					"action": "test",
				},
			}

			widgets.HandleViewResult(result, nil, onCancel)
			Expect(cancelCalled).To(BeFalse())
		})
	})

	Describe("Other result types", func() {
		It("returns nil for ResultSubmit", func() {
			result := &widgets.SubmitViewResult{
				FormData: map[string]string{"name": "test"},
			}

			cmd := widgets.HandleViewResult(result, nil, nil)
			Expect(cmd).To(BeNil())
		})

		It("returns nil for ResultError", func() {
			result := &widgets.ErrorViewResult{
				Message: "error occurred",
			}

			cmd := widgets.HandleViewResult(result, nil, nil)
			Expect(cmd).To(BeNil())
		})
	})
})
