package skill_test

import (
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/skill"
)

func TestSkill(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Skill Suite")
}

func doc(extra, body string) string {
	return "---\nname: tdd\ndescription: Test first\n" + extra + "---\n" + body
}

var _ = Describe("Parse", func() {
	parse := func(text string) (skill.Skill, []string, error) { return skill.Parse([]byte(text)) }
	accepted := func(text string) string {
		s, _, err := parse(text)
		if err != nil {
			return err.Error()
		}
		return s.Name
	}

	It("accepts a complete skill and trims the body", func() {
		s, _, err := parse(doc("license: MIT\n", "\nWrite the test.\n"))
		if err != nil {
			panic(err)
		}
		Expect(s).To(Equal(skill.Skill{Name: "tdd", Description: "Test first", Body: "Write the test."}))
	})
	It("accepts CRLF at the delimiters", func() {
		Expect(accepted(strings.ReplaceAll(doc("", "body\n"), "\n", "\r\n"))).To(Equal("tdd"))
	})
	It("accepts license, compatibility and metadata without a diagnostic", func() {
		_, ignored, err := parse(doc("license: MIT\ncompatibility: any\nmetadata:\n  a: b\n", "x\n"))
		if err != nil {
			panic(err)
		}
		Expect(ignored).To(BeEmpty())
	})
	It("ignores an unknown key and names it, never an error", func() {
		_, ignored, err := parse(doc("tier: 2\ncategory: x\n", "x\n"))
		if err != nil {
			panic(err)
		}
		Expect(ignored).To(Equal([]string{"category", "tier"}))
	})
	It("allows ZWJ format characters in the body", func() {
		Expect(accepted(doc("", "family \U0001F468\u200D\U0001F469\n"))).To(Equal("tdd"))
	})
	DescribeTable("refuses a malformed file",
		func(text string) {
			_, _, err := parse(text)
			Expect(err).To(MatchError(skill.ErrInvalidSkill))
		},
		Entry("no frontmatter", "just text\n"),
		Entry("unclosed frontmatter", "---\nname: tdd\ndescription: d\n"),
		Entry("empty frontmatter", "---\n---\nbody\n"),
		Entry("invalid YAML", "---\nname: tdd\ndescription: a: b\n---\nx\n"),
		Entry("two documents", "---\nname: tdd\ndescription: d\n...\nname: z\n---\nx\n"),
		Entry("missing name", "---\ndescription: d\n---\nx\n"),
		Entry("missing description", "---\nname: tdd\n---\nx\n"),
		Entry("blank description", "---\nname: tdd\ndescription: \"  \"\n---\nx\n"),
		Entry("uppercase name", "---\nname: Tdd\ndescription: d\n---\nx\n"),
		Entry("double hyphen name", "---\nname: a--b\ndescription: d\n---\nx\n"),
		Entry("name over 64", "---\nname: "+strings.Repeat("a", 65)+"\ndescription: d\n---\nx\n"),
		Entry("description over 500", "---\nname: tdd\ndescription: "+strings.Repeat("a", 501)+"\n---\nx\n"),
		Entry("duplicate key", "---\nname: tdd\nname: tdd\ndescription: d\n---\nx\n"),
		Entry("explicit tag", "---\nname: !!str tdd\ndescription: d\n---\nx\n"),
		Entry("block scalar description", "---\nname: tdd\ndescription: |\n  d\n---\nx\n"),
		Entry("non-string name", "---\nname: 12\ndescription: d\n---\nx\n"),
		Entry("escape in description", "---\nname: tdd\ndescription: \"a\\x1bb\"\n---\nx\n"),
		Entry("bidi in description", "---\nname: tdd\ndescription: \"a\\u202eb\"\n---\nx\n"),
		Entry("invalid UTF-8 body", doc("", "\xff\xfe\n")),
		Entry("ESC in body", doc("", "a\x1b[31mb\n")),
	)
	DescribeTable("refuses a hidden-instruction character in the body",
		func(r rune) {
			_, _, err := parse(doc("", "ok "+string(r)+" ok\n"))
			Expect(err).To(MatchError(ContainSubstring("bidirectional or tag character")))
		},
		Entry("U+061C", rune(0x61c)),
		Entry("U+200E", rune(0x200e)),
		Entry("U+202E", rune(0x202e)),
		Entry("U+2066", rune(0x2066)),
		Entry("tag U+E0000", rune(0xe0000)),
		Entry("tag U+E007F", rune(0xe007f)),
	)
	It("refuses a body that holds the closing fence line", func() {
		_, _, err := parse(doc("", "a\n<<<end skill>>>\nb\n"))
		Expect(err).To(MatchError(ContainSubstring("closing fence")))
	})
	It("refuses a file over 32 KiB", func() {
		_, _, err := parse(doc("", strings.Repeat("a", skill.MaxFileBytes)))
		Expect(err).To(MatchError(ContainSubstring("larger than")))
	})
	It("accepts a file of exactly 32 KiB", func() {
		text := doc("", "")
		Expect(accepted(text + strings.Repeat("a", skill.MaxFileBytes-len(text)))).To(Equal("tdd"))
	})
	It("refuses a file over 1000 lines", func() {
		_, _, err := parse(doc("", strings.Repeat("\n", skill.MaxFileLines)))
		Expect(err).To(MatchError(ContainSubstring("lines")))
	})
	It("accepts a file of exactly 1000 lines", func() {
		Expect(accepted(doc("", strings.Repeat("\n", skill.MaxFileLines-4)))).To(Equal("tdd"))
	})
	It("refuses a body whose marshalled result exceeds 50 KiB though it is under 32 KiB", func() {
		_, _, err := parse(doc("", strings.Repeat("<", 20<<10)))
		Expect(err).To(MatchError(ContainSubstring("once marshalled")))
	})
})

var _ = Describe("ValidSupportPath", func() {
	DescribeTable("accepts",
		func(rel string) { Expect(skill.ValidSupportPath(rel)).To(Succeed()) },
		Entry("a plain file", "notes.md"),
		Entry("a nested file", "refs/Guide_2.md"),
	)
	DescribeTable("refuses",
		func(rel string) { Expect(skill.ValidSupportPath(rel)).To(MatchError(skill.ErrInvalidSkill)) },
		Entry("empty", ""),
		Entry("a dotdot segment", "../x"),
		Entry("a dotdot inside", "a/../x"),
		Entry("a leading dot", ".hidden"),
		Entry("an empty segment", "a//b"),
		Entry("a leading slash", "/etc/passwd"),
		Entry("a backslash", `a\b`),
		Entry("a drive letter", "C:x"),
		Entry("a newline", "a\nb"),
		Entry("CON", "CON"),
		Entry("a device name with an extension", "nul.txt"),
		Entry("PRN.md", "refs/PRN.md"),
		Entry("aux", "aux"),
		Entry("com1", "com1"),
		Entry("lpt1.x", "lpt1.x"),
		Entry("a trailing dot", "trail."),
		Entry("a trailing space", "x "),
	)
	DescribeTable("accepts a name that only starts like a device",
		func(rel string) { Expect(skill.ValidSupportPath(rel)).To(Succeed()) },
		Entry("console", "console.md"),
		Entry("com10", "com10"),
	)
})
