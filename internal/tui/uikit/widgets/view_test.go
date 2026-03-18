package widgets_test

import (
	"errors"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

// fakeView is a test double implementing the View interface.
type fakeView struct {
	content  string
	helpText string
}

func (f *fakeView) RenderContent() string                          { return f.content }
func (f *fakeView) HelpText() string                               { return f.helpText }
func (f *fakeView) Init() tea.Cmd                                  { return nil }
func (f *fakeView) Update(_ tea.Msg) (tea.Cmd, widgets.ViewResult) { return nil, nil }

// fakeModule is a test double implementing the Module interface.
type fakeModule struct {
	content  string
	helpText string
}

func (m *fakeModule) RenderContent(_ widgets.View) string { return m.content }
func (m *fakeModule) HelpText(_ widgets.View) string      { return m.helpText }

var _ = Describe("Contract", func() {
	Describe("View interface", func() {
		var v widgets.View

		BeforeEach(func() {
			v = &fakeView{content: "hello", helpText: "↑/↓ navigate"}
		})

		It("returns content from RenderContent", func() {
			Expect(v.RenderContent()).To(Equal("hello"))
		})

		It("returns help text from HelpText", func() {
			Expect(v.HelpText()).To(Equal("↑/↓ navigate"))
		})

		It("returns nil from Init", func() {
			Expect(v.Init()).To(BeNil())
		})

		It("returns nil result from Update when no state change", func() {
			_, result := v.Update(nil)
			Expect(result).To(BeNil())
		})
	})

	Describe("NoOpViewResult", func() {
		It("returns nil", func() {
			Expect(widgets.NoOpViewResult()).To(BeNil())
		})
	})

	Describe("NavigateViewResult", func() {
		var r widgets.ViewResult

		BeforeEach(func() {
			r = &widgets.NavigateViewResult{ResultData: "selected-item"}
		})

		It("has type ResultNavigate", func() {
			Expect(r.Type()).To(Equal(widgets.ResultNavigate))
		})

		It("returns the navigation payload", func() {
			Expect(r.Data()).To(Equal("selected-item"))
		})

		It("returns empty metadata by default", func() {
			Expect(r.Metadata()).To(BeEmpty())
		})

		It("supports fluent metadata", func() {
			r = r.WithMetadata("scroll", 42)
			Expect(r.Metadata()).To(HaveKeyWithValue("scroll", 42))
		})
	})

	Describe("CancelViewResult", func() {
		var r widgets.ViewResult

		BeforeEach(func() {
			r = &widgets.CancelViewResult{}
		})

		It("has type ResultCancel", func() {
			Expect(r.Type()).To(Equal(widgets.ResultCancel))
		})

		It("returns nil data", func() {
			Expect(r.Data()).To(BeNil())
		})

		It("returns empty metadata by default", func() {
			Expect(r.Metadata()).To(BeEmpty())
		})

		It("supports fluent metadata", func() {
			r = r.WithMetadata("reason", "user-cancelled")
			Expect(r.Metadata()).To(HaveKeyWithValue("reason", "user-cancelled"))
		})
	})

	Describe("SubmitViewResult", func() {
		var r widgets.ViewResult

		BeforeEach(func() {
			r = &widgets.SubmitViewResult{FormData: map[string]string{"name": "Alice"}}
		})

		It("has type ResultSubmit", func() {
			Expect(r.Type()).To(Equal(widgets.ResultSubmit))
		})

		It("returns the form data", func() {
			Expect(r.Data()).To(Equal(map[string]string{"name": "Alice"}))
		})

		It("returns empty metadata by default", func() {
			Expect(r.Metadata()).To(BeEmpty())
		})

		It("supports fluent metadata", func() {
			r = r.WithMetadata("timestamp", int64(1234567890))
			Expect(r.Metadata()).To(HaveKeyWithValue("timestamp", int64(1234567890)))
		})
	})

	Describe("ErrorViewResult", func() {
		var r widgets.ViewResult

		BeforeEach(func() {
			r = &widgets.ErrorViewResult{Err: errors.New("boom"), Message: "something failed"}
		})

		It("has type ResultError", func() {
			Expect(r.Type()).To(Equal(widgets.ResultError))
		})

		It("returns error and message in data map", func() {
			data, ok := r.Data().(map[string]interface{})
			Expect(ok).To(BeTrue())
			Expect(data["message"]).To(Equal("something failed"))
			Expect(data["error"].(error)).To(MatchError("boom"))
		})

		It("returns empty metadata by default", func() {
			Expect(r.Metadata()).To(BeEmpty())
		})

		It("supports fluent metadata", func() {
			r = r.WithMetadata("retry_count", 3)
			Expect(r.Metadata()).To(HaveKeyWithValue("retry_count", 3))
		})
	})

	Describe("Module interface", func() {
		var m widgets.Module
		var v widgets.View

		BeforeEach(func() {
			v = &fakeView{content: "original", helpText: "original help"}
			m = &fakeModule{content: "modal content", helpText: "modal help"}
		})

		It("overrides RenderContent", func() {
			Expect(m.RenderContent(v)).To(Equal("modal content"))
		})

		It("overrides HelpText", func() {
			Expect(m.HelpText(v)).To(Equal("modal help"))
		})
	})
})
