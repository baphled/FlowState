package validation_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/validation"
)

func sampleEvent(seq int64) validation.PersistedEvent {
	payload := map[string]any{"turn": float64(seq)}
	_, digest := validation.CanonicalPayload(payload)
	return validation.PersistedEvent{
		SessionID: "sess-1", Seq: seq, CreatedAtUTC: "2026-10-30T00:00:00Z",
		Actor: "user", Type: "turn.start", PayloadSHA256: digest, Payload: payload,
	}
}

var _ = Describe("CompareProjection", func() {
	It("accepts stdout that matches the persisted projection in order", func() {
		first := sampleEvent(0)
		second := sampleEvent(1)
		one, err := validation.ProjectionLine(first)
		Expect(err).NotTo(HaveOccurred())
		two, err := validation.ProjectionLine(second)
		Expect(err).NotTo(HaveOccurred())
		Expect(validation.CompareProjection([]validation.PersistedEvent{first, second}, []string{one, two})).To(BeEmpty())
	})

	It("rejects stdout whose order differs from persisted seq order", func() {
		first := sampleEvent(0)
		second := sampleEvent(1)
		one, err := validation.ProjectionLine(first)
		Expect(err).NotTo(HaveOccurred())
		two, err := validation.ProjectionLine(second)
		Expect(err).NotTo(HaveOccurred())
		Expect(validation.CompareProjection([]validation.PersistedEvent{first, second}, []string{two, one})).To(ContainElement(ContainSubstring("differs")))
	})

	It("rejects a projection line for a persisted payload whose hash does not match its canonical bytes", func() {
		event := sampleEvent(0)
		event.PayloadSHA256 = event.PayloadSHA256[:63] + "b"
		Expect(validation.ValidateEventLog([]validation.PersistedEvent{event})).To(ContainElement(ContainSubstring("corrupt payload hash")))
	})

	It("rejects an extra untyped final stdout object", func() {
		event := sampleEvent(0)
		one, err := validation.ProjectionLine(event)
		Expect(err).NotTo(HaveOccurred())
		Expect(validation.CompareProjection([]validation.PersistedEvent{event}, []string{one, `{"status":"done"}`})).To(ContainElement(ContainSubstring("count")))
	})

	It("rejects missing stdout lines", func() {
		Expect(validation.CompareProjection([]validation.PersistedEvent{sampleEvent(0)}, nil)).To(ContainElement(ContainSubstring("count")))
	})
})

var _ = Describe("ValidateEventLog", func() {
	It("accepts a contiguous sequence from 0 with matching payload hashes", func() {
		Expect(validation.ValidateEventLog([]validation.PersistedEvent{sampleEvent(0), sampleEvent(1)})).To(BeEmpty())
	})

	It("rejects a sequence gap (corrupt sequence)", func() {
		Expect(validation.ValidateEventLog([]validation.PersistedEvent{sampleEvent(0), sampleEvent(2)})).To(ContainElement(ContainSubstring("seq gap")))
	})

	It("rejects a corrupt payload hash", func() {
		event := sampleEvent(0)
		event.PayloadSHA256 = hexify(64)
		Expect(validation.ValidateEventLog([]validation.PersistedEvent{event})).To(ContainElement(ContainSubstring("corrupt payload hash")))
	})
})

var _ = Describe("ValidateExit", func() {
	It("accepts a completed run exiting 0", func() {
		Expect(validation.ValidateExit(validation.ExitCompleted, 0)).To(BeEmpty())
	})

	It("accepts a provider failure exiting its expected 3", func() {
		Expect(validation.ValidateExit(validation.ExitProvider, 3)).To(BeEmpty())
	})

	It("accepts a budget pause exiting its expected 5", func() {
		Expect(validation.ValidateExit(validation.ExitBudgetPause, 5)).To(BeEmpty())
	})

	It("accepts a SIGINT cancel exiting its expected 130", func() {
		Expect(validation.ValidateExit(validation.ExitCancelled, 130)).To(BeEmpty())
	})

	It("accepts a terminal refusal exiting its expected 6", func() {
		Expect(validation.ValidateExit(validation.ExitTerminalRefuse, 6)).To(BeEmpty())
	})

	It("rejects an unexpected exit differing from the expected matrix exit", func() {
		Expect(validation.ValidateExit(validation.ExitBudgetPause, 0)).To(ContainElement(ContainSubstring("unexpected exit")))
	})

	It("rejects a usage error exiting anything other than 2", func() {
		Expect(validation.ValidateExit(validation.ExitUsage, 1)).To(ContainElement(ContainSubstring("expected 2 got 1")))
	})
})

var _ = Describe("ExitMatrix", func() {
	It("declares the stable completed exit 0 for session run", func() {
		Expect(validation.ExitMatrix).To(ContainElement(matchingExit("session run", "completed", validation.ExitCompleted)))
	})

	It("declares SIGINT cancel as 130", func() {
		Expect(validation.ExitMatrix).To(ContainElement(matchingExit("session run", "cancelled", validation.ExitCancelled)))
	})

	It("declares unknown session as 1", func() {
		Expect(validation.ExitMatrix).To(ContainElement(matchingExit("session events", "session_not_found", validation.ExitUnknownSession)))
	})

	It("declares replay mismatch as 7", func() {
		Expect(validation.ExitMatrix).To(ContainElement(matchingExit("replay", "replay_mismatch", validation.ExitReplayMismatch)))
	})
})

func matchingExit(command, failure string, exit validation.ExitCode) any {
	return WithTransform(func(row validation.ExitExpectation) bool {
		return row.Command == command && row.Error == failure && row.Exit == exit
	}, BeTrue())
}
