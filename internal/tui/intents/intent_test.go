package intents_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/tui/intents"
)

var _ = Describe("IntentResult", func() {
	It("can be created with Data field", func() {
		result := &intents.IntentResult{
			Data: "test-data",
		}
		Expect(result).NotTo(BeNil())
		Expect(result.Data).To(Equal("test-data"))
	})

	It("can be created with Action field", func() {
		result := &intents.IntentResult{
			Action: "test-action",
		}
		Expect(result).NotTo(BeNil())
		Expect(result.Action).To(Equal("test-action"))
	})

	It("can be created with Error field", func() {
		testErr := goterror("test error")
		result := &intents.IntentResult{
			Error: testErr,
		}
		Expect(result).NotTo(BeNil())
		Expect(result.Error).To(Equal(testErr))
	})

	It("can be created with all fields", func() {
		testErr := goterror("test error")
		result := &intents.IntentResult{
			Data:   "test-data",
			Action: "test-action",
			Error:  testErr,
		}
		Expect(result).NotTo(BeNil())
		Expect(result.Data).To(Equal("test-data"))
		Expect(result.Action).To(Equal("test-action"))
		Expect(result.Error).To(Equal(testErr))
	})
})

var _ = Describe("ShowModalMsg", func() {
	It("can be created with Modal field", func() {
		intent := &mockIntent{}
		msg := intents.ShowModalMsg{
			Modal: intent,
		}
		Expect(msg.Modal).To(Equal(intent))
	})
})

var _ = Describe("DismissModalMsg", func() {
	It("zero-value is valid", func() {
		msg := intents.DismissModalMsg{}
		Expect(msg).NotTo(BeNil())
	})
})

var _ = Describe("SwitchToIntentMsg", func() {
	It("can be created with Intent field", func() {
		intent := &mockIntent{}
		msg := intents.SwitchToIntentMsg{
			Intent: intent,
		}
		Expect(msg.Intent).To(Equal(intent))
	})
})

var _ = Describe("Intent interface", func() {
	It("is satisfied by a mock implementation", func() {
		mock := &mockIntent{}
		var _ intents.Intent = mock
	})
})

type mockIntent struct{}

func (m *mockIntent) Init() tea.Cmd {
	return nil
}

func (m *mockIntent) Update(msg tea.Msg) tea.Cmd {
	return nil
}

func (m *mockIntent) View() string {
	return ""
}

func (m *mockIntent) Result() *intents.IntentResult {
	return nil
}

func goterror(msg string) error {
	return &testError{msg: msg}
}

type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}
