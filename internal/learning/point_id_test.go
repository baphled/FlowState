package learning_test

import (
	"github.com/baphled/flowstate/internal/learning"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("PointIDFromSource", func() {
	It("returns a valid UUID string (36 chars with hyphens) for a session-prefixed source ID", func() {
		id := learning.PointIDFromSource("session-1776075781962028658")
		Expect(id).To(HaveLen(36))
		Expect(id).To(MatchRegexp(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`))
	})

	It("returns a valid UUID string for a numeric snowflake source ID", func() {
		id := learning.PointIDFromSource("1776075802192788529")
		Expect(id).To(HaveLen(36))
		Expect(id).To(MatchRegexp(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`))
	})

	It("is deterministic: the same source ID always produces the same UUID", func() {
		a := learning.PointIDFromSource("session-1776075781962028658")
		b := learning.PointIDFromSource("session-1776075781962028658")
		Expect(a).To(Equal(b))
	})

	It("produces distinct UUIDs for distinct source IDs", func() {
		a := learning.PointIDFromSource("session-1776075781962028658")
		b := learning.PointIDFromSource("session-1776075781962028659")
		Expect(a).NotTo(Equal(b))
	})

	It("produces a deterministic UUID for an empty source ID", func() {
		a := learning.PointIDFromSource("")
		b := learning.PointIDFromSource("")
		Expect(a).To(Equal(b))
		Expect(a).To(HaveLen(36))
	})
})
