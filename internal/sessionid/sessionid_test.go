package sessionid_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/sessionid"
)

var _ = Describe("Validate", func() {
	// Positive contract: IDs composed of alphanumerics, dashes, and
	// underscores (the shape UUIDs, slugs, and operator-chosen tokens
	// all satisfy) are accepted so legitimate workflows are not broken
	// by the path-traversal fix.
	DescribeTable("accepts safe session IDs",
		func(id string) {
			Expect(sessionid.Validate(id)).To(Succeed())
		},
		Entry("simple lowercase", "abc"),
		Entry("hyphenated", "session-123"),
		Entry("underscored", "session_123"),
		Entry("UUID v4", "0d4e6b4e-1b4b-4e1b-9e1b-4b1b4e1b4b1b"),
		Entry("dot in middle (away from prefix)", "SessionWith.dot.in.middle"),
		Entry("single character", "a"),
		Entry("mixed case + digits + separators", "UPPER_and_lower-123"),
	)

	// Rejection contract: every rule from the H4 audit (empty, slashes,
	// leading dot that hides path-escape inside ".." and ".hidden",
	// absolute paths, and ".." anywhere as a path component) returns
	// ErrInvalidSessionID so the caller can errors.Is on it uniformly.
	DescribeTable("rejects unsafe session IDs with ErrInvalidSessionID",
		func(id string) {
			err := sessionid.Validate(id)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, sessionid.ErrInvalidSessionID)).To(BeTrue(),
				"Validate(%q) err = %v; want errors.Is ErrInvalidSessionID", id, err)
		},
		Entry("empty", ""),
		Entry("whitespace-only", "   "),
		Entry("tab-only", "\t"),
		Entry("forward-slash", "foo/bar"),
		Entry("backslash", "foo\\bar"),
		Entry("leading-dot", ".hidden"),
		Entry("dotdot-only", ".."),
		Entry("dotdot-prefix", "../evil"),
		Entry("dotdot-suffix", "evil/.."),
		Entry("dotdot-middle", "a/../b"),
		Entry("absolute-unix", "/tmp/evil"),
		Entry("absolute-windows", "C:\\evil"),
		Entry("null-byte", "a\x00b"),
		Entry("path-traversal-real", "../../tmp/evil"),
	)
})
