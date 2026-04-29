package cli

import (
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("resolveChatSessionID", func() {
	Context("when no session ID is supplied", func() {
		It("returns a generated UUID v4 with no error", func() {
			id, err := resolveChatSessionID("")

			Expect(err).NotTo(HaveOccurred())
			Expect(id).NotTo(BeEmpty())
			Expect(id).To(MatchRegexp(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`),
				"generated id must be UUID v4 — matches the canonical session-id format")
		})
	})

	Context("when a valid session ID is supplied", func() {
		It("returns the supplied id unchanged", func() {
			id, err := resolveChatSessionID("alpha-numeric-id-42")

			Expect(err).NotTo(HaveOccurred())
			Expect(id).To(Equal("alpha-numeric-id-42"))
		})

		It("accepts canonical UUID v4 ids", func() {
			id, err := resolveChatSessionID("3b2e1a4c-9d6f-4e7c-8a5b-1f0c8d3e7a4b")

			Expect(err).NotTo(HaveOccurred())
			Expect(id).To(Equal("3b2e1a4c-9d6f-4e7c-8a5b-1f0c8d3e7a4b"))
		})
	})

	Context("when the session ID would escape the sessions directory", func() {
		DescribeTable("rejects path-traversal-shaped ids",
			func(input string) {
				id, err := resolveChatSessionID(input)

				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, errInvalidSessionID)).To(BeTrue(),
					"rejection must use the typed sentinel so callers can branch")
				Expect(id).To(BeEmpty())
			},
			Entry("forward slash", "../etc/passwd"),
			Entry("backslash", `..\..\Windows\System32`),
			Entry("nested forward slash", "subdir/session"),
			Entry("absolute path", "/tmp/session"),
		)
	})

	Context("when the session ID would create a hidden file", func() {
		It("rejects ids with a leading dot", func() {
			id, err := resolveChatSessionID(".hidden-session")

			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, errInvalidSessionID)).To(BeTrue())
			Expect(id).To(BeEmpty())
		})

		It("accepts a dot in the middle of the id", func() {
			id, err := resolveChatSessionID("session.v2")

			Expect(err).NotTo(HaveOccurred())
			Expect(id).To(Equal("session.v2"))
		})
	})

	Context("when the session ID exceeds the length cap", func() {
		It("rejects ids longer than maxSessionIDLength", func() {
			tooLong := strings.Repeat("a", maxSessionIDLength+1)

			id, err := resolveChatSessionID(tooLong)

			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, errInvalidSessionID)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("max is 256"))
			Expect(id).To(BeEmpty())
		})

		It("accepts ids exactly at the length cap", func() {
			atCap := strings.Repeat("a", maxSessionIDLength)

			id, err := resolveChatSessionID(atCap)

			Expect(err).NotTo(HaveOccurred())
			Expect(id).To(Equal(atCap))
		})
	})

	Context("when the session ID contains control characters", func() {
		DescribeTable("rejects non-printable characters",
			func(input string) {
				id, err := resolveChatSessionID(input)

				Expect(err).To(HaveOccurred())
				Expect(errors.Is(err, errInvalidSessionID)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("non-printable"))
				Expect(id).To(BeEmpty())
			},
			Entry("null byte", "session\x00"),
			Entry("newline", "session\nid"),
			Entry("tab", "session\tid"),
			Entry("escape", "session\x1bid"),
			Entry("DEL", "session\x7f"),
		)

		It("accepts unicode letters/digits", func() {
			id, err := resolveChatSessionID("séssion-Δ-42")

			Expect(err).NotTo(HaveOccurred())
			Expect(id).To(Equal("séssion-Δ-42"))
		})
	})
})
