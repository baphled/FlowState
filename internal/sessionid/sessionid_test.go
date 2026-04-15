package sessionid_test

import (
	"errors"
	"testing"

	"github.com/baphled/flowstate/internal/sessionid"
)

// TestValidate_AcceptsSafeIDs pins the positive contract: IDs composed
// of alphanumerics, dashes, and underscores (the shape UUIDs, slugs,
// and operator-chosen tokens all satisfy) are accepted so legitimate
// workflows are not broken by the path-traversal fix.
func TestValidate_AcceptsSafeIDs(t *testing.T) {
	t.Parallel()

	cases := []string{
		"abc",
		"session-123",
		"session_123",
		"0d4e6b4e-1b4b-4e1b-9e1b-4b1b4e1b4b1b", // UUID v4
		"SessionWith.dot.in.middle",             // dot is fine away from the prefix
		"a",
		"UPPER_and_lower-123",
	}

	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			if err := sessionid.Validate(id); err != nil {
				t.Fatalf("Validate(%q) = %v; want nil", id, err)
			}
		})
	}
}

// TestValidate_RejectsUnsafeIDs pins the rejection contract: every
// rule from the H4 audit — empty, slashes, leading dot (hides path-
// escape inside .. and .hidden), absolute paths, and .. anywhere as
// a path component — returns ErrInvalidSessionID so the caller can
// errors.Is on it uniformly.
func TestValidate_RejectsUnsafeIDs(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"empty":               "",
		"whitespace-only":     "   ",
		"tab-only":            "\t",
		"forward-slash":       "foo/bar",
		"backslash":           "foo\\bar",
		"leading-dot":         ".hidden",
		"dotdot-only":         "..",
		"dotdot-prefix":       "../evil",
		"dotdot-suffix":       "evil/..",
		"dotdot-middle":       "a/../b",
		"absolute-unix":       "/tmp/evil",
		"absolute-windows":    "C:\\evil",
		"null-byte":           "a\x00b",
		"path-traversal-real": "../../tmp/evil",
	}

	for name, id := range cases {
		t.Run(name, func(t *testing.T) {
			err := sessionid.Validate(id)
			if err == nil {
				t.Fatalf("Validate(%q) = nil; want error", id)
			}
			if !errors.Is(err, sessionid.ErrInvalidSessionID) {
				t.Fatalf("Validate(%q) err = %v; want errors.Is ErrInvalidSessionID", id, err)
			}
		})
	}
}
