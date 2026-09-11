package engine

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/swarm"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("appendGateDirective", func() {
	DescribeTable("directive construction",
		func(message string, gateErr error, matcher func(string)) {
			matcher(appendGateDirective(message, gateErr))
		},
		Entry("no-output failure mentions coordination_store",
			"original prompt", &swarm.GateError{
				GateName: "post-member-explorer-evidence-grounding",
				Reason:   "no member output found at [dd-swarm/explorer/output]: the member did not write its output — re-delegate with an explicit coordination_store write.",
			},
			func(out string) {
				Expect(out).To(ContainSubstring("coordination_store"))
				Expect(out).To(ContainSubstring("post-member-explorer-evidence-grounding"))
				Expect(out).To(ContainSubstring("original prompt"))
			}),
		Entry("schema-validation failure carries gate name and reason",
			"original prompt", &swarm.GateError{
				GateName: "post-member-writer-depth-budget",
				Reason:   "Your response was 120 words, minimum 300 words.",
			},
			func(out string) {
				Expect(out).To(ContainSubstring("post-member-writer-depth-budget"))
				Expect(out).To(ContainSubstring("120 words"))
			}),
		Entry("empty Reason returns message unchanged",
			"original prompt", &swarm.GateError{Reason: ""},
			func(out string) {
				Expect(out).To(Equal("original prompt"))
			}),
		Entry("nil error returns message unchanged",
			"original prompt", nil,
			func(out string) {
				Expect(out).To(Equal("original prompt"))
			}),
		Entry("non-GateError returns message unchanged",
			"original prompt", errors.New("plain error"),
			func(out string) {
				Expect(out).To(Equal("original prompt"))
			}),
		Entry("wrapped GateError is unwrapped via errors.As",
			"original prompt", fmt.Errorf("wrapped: %w", &swarm.GateError{
				GateName: "gate-x",
				Reason:   "shortfall reason",
			}),
			func(out string) {
				Expect(out).To(ContainSubstring("gate-x"))
				Expect(out).To(ContainSubstring("shortfall reason"))
			}),
	)

	It("appends directive on a fresh paragraph when message is non-empty", func() {
		out := appendGateDirective("prompt", &swarm.GateError{
			GateName: "g",
			Reason:   "r",
		})
		Expect(out).To(ContainSubstring("prompt\n\nGate"))
	})

	It("returns the directive alone when message is empty", func() {
		out := appendGateDirective("", &swarm.GateError{
			GateName: "g",
			Reason:   "r",
		})
		Expect(out).To(HavePrefix("Gate 'g' rejected your output."))
	})
})

// gateErrForTest builds a typed gate error the way the swarm runner
// does, so the seam exercises the real directive path.
func gateErrForTest() error {
	return &swarm.GateError{GateName: "result-schema", Reason: "output missing findings"}
}

// TestAppendGateDirectiveAppends asserts the exported seam appends the
// gate-retry directive to a non-empty message on a fresh paragraph.
func TestAppendGateDirectiveAppends(t *testing.T) {
	got := AppendGateDirective("analyse the code", gateErrForTest())
	if !strings.HasPrefix(got, "analyse the code") {
		t.Fatalf("got %q, want original message preserved", got)
	}
	if !strings.Contains(got, "result-schema") {
		t.Fatalf("got %q, want gate name in directive", got)
	}
}

// TestAppendGateDirectiveEmptyMessage asserts the seam returns the
// directive alone when the original message is empty.
func TestAppendGateDirectiveEmptyMessage(t *testing.T) {
	got := AppendGateDirective("", gateErrForTest())
	if !strings.Contains(got, "result-schema") {
		t.Fatalf("got %q, want directive alone", got)
	}
}

// TestAppendGateDirectiveIgnoresNonGateError asserts a plain error
// leaves the message unchanged; only *swarm.GateError triggers the
// directive path.
func TestAppendGateDirectiveIgnoresNonGateError(t *testing.T) {
	msg := "plain message"
	got := AppendGateDirective(msg, errors.New("ordinary"))
	if got != msg {
		t.Fatalf("got %q, want unchanged %q", got, msg)
	}
}

// Notification publisher specs — restored after the test-file
// consolidation accident discarded delegation_events_notifications_test.go.
// publishNotification projects a delegation lifecycle payload onto the
// `notification` bus topic: turn_complete/info for clean completion,
// task_failed/error with the reason in the message for failures, and
// a silent no-op when no bus is wired.
var _ = Describe("DelegateTool.publishNotification", func() {
	nonNilBusData := events.DelegationEventData{
		ChainID:         "chain-notif-1",
		ParentSessionID: "parent-1",
		ChildSessionID:  "child-1",
		SourceAgent:     "orchestrator",
		TargetAgent:     "qa-agent",
		ProviderName:    "anthropic",
		ModelName:       "claude-sonnet-4-6",
	}

	It("publishes turn_complete with info severity and attribution", func() {
		bus := eventbus.NewEventBus()
		d := &DelegateTool{eventBus: bus}

		var notif *events.NotificationEvent
		bus.Subscribe(events.EventNotification, func(event any) {
			notif = event.(*events.NotificationEvent)
		})

		d.publishNotification(events.NotificationTypeTurnComplete, nonNilBusData, "")

		Expect(notif).NotTo(BeNil())
		Expect(notif.Data.Type).To(Equal(events.NotificationTypeTurnComplete))
		Expect(notif.Data.Severity).To(Equal(events.NotificationSeverityInfo))
		Expect(notif.Data.ID).To(Equal("chain-notif-1"))
		Expect(notif.Data.Provider).To(Equal("anthropic"))
		Expect(notif.Data.Model).To(Equal("claude-sonnet-4-6"))
		Expect(notif.Data.Message).To(ContainSubstring("qa-agent"))
		Expect(notif.Data.Message).To(ContainSubstring("completed"))
	})

	It("publishes task_failed with error severity and the reason in the message", func() {
		bus := eventbus.NewEventBus()
		d := &DelegateTool{eventBus: bus}

		var notif *events.NotificationEvent
		bus.Subscribe(events.EventNotification, func(event any) {
			notif = event.(*events.NotificationEvent)
		})

		d.publishNotification(events.NotificationTypeTaskFailed, nonNilBusData, "provider exploded")

		Expect(notif).NotTo(BeNil())
		Expect(notif.Data.Type).To(Equal(events.NotificationTypeTaskFailed))
		Expect(notif.Data.Severity).To(Equal(events.NotificationSeverityError))
		Expect(notif.Data.Message).To(ContainSubstring("provider exploded"))
	})

	It("is a no-op when no event bus is wired", func() {
		d := &DelegateTool{}
		Expect(func() {
			d.publishNotification(events.NotificationTypeTurnComplete, nonNilBusData, "")
		}).NotTo(Panic())
	})
})
