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
