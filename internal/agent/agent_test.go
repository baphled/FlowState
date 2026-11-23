package agent_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
)

func TestAgent(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Agent Profile Suite")
}

var known = []string{"echo", "fs.read", "fs.write", "fs.edit", "exec.run", agent.DelegateTool}

const reviewer = "---\nname: reviewer\ndescription: Reads a change\ntools: [fs.read]\nmodel: inherit\nmax_turns: 25\n---\nYou review.\n"

// put writes name below dir and returns its path.
func put(dir, name, content string) string {
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		panic(err)
	}
	return path
}

// hiddenEntries is one table entry per field and hidden character (a YAML escape).
func hiddenEntries() []TableEntry {
	var entries []TableEntry
	for _, field := range []string{"description", "model"} {
		for _, c := range []string{"x7f", "u009b", "u0085", "u2028", "u202e", "u200b", "ufeff"} {
			entries = append(entries, Entry(field+" \\"+c, field, "\\"+c))
		}
	}
	return entries
}

// profileText builds a profile with the given extra frontmatter lines.
func profileText(header, body string) string {
	return "---\nname: a\ndescription: d\n" + header + "---\n" + body
}

var _ = Describe("Parse", func() {
	It("accepts a complete profile", func() {
		p, err := agent.Parse([]byte(reviewer), known)
		if err != nil {
			panic(err)
		}
		Expect(p).To(Equal(agent.Profile{Name: "reviewer", Description: "Reads a change", Tools: []string{"fs.read"},
			Model: "inherit", MaxTurns: 25, Body: "You review."}))
	})
	It("defaults the model, the turns and the tools", func() {
		p, err := agent.Parse([]byte(profileText("", "Do it.")), known)
		if err != nil {
			panic(err)
		}
		Expect(p).To(Equal(agent.Profile{Name: "a", Description: "d", Tools: []string{}, Model: "inherit",
			MaxTurns: 25, Body: "Do it."}))
	})
	It("accepts the reserved delegate tool", func() {
		p, err := agent.Parse([]byte(profileText("tools: [delegate]\n", "Do it.")), known)
		if err != nil {
			panic(err)
		}
		Expect(p.Tools).To(Equal([]string{"delegate"}))
	})
	It("accepts the largest max_turns", func() {
		p, err := agent.Parse([]byte(profileText("max_turns: 100\n", "Do it.")), known)
		if err != nil {
			panic(err)
		}
		Expect(p.MaxTurns).To(Equal(100))
	})

	It("accepts a tab in the description", func() {
		p, err := agent.Parse([]byte("---\nname: a\ndescription: \"d\\te\"\n---\nx\n"), known)
		if err != nil {
			panic(err)
		}
		Expect(p.Description).To(Equal("d\te"))
	})
	It("accepts newlines and tabs in the body", func() {
		p, err := agent.Parse([]byte(profileText("", "a\n\tb\n")), known)
		if err != nil {
			panic(err)
		}
		Expect(p.Body).To(Equal("a\n\tb"))
	})
	It("accepts a CRLF profile", func() {
		text := strings.ReplaceAll(reviewer, "\n", "\r\n")
		p, err := agent.Parse([]byte(text), known)
		if err != nil {
			panic(err)
		}
		Expect(p.Name).To(Equal("reviewer"))
	})
	It("accepts a document end marker with nothing after it", func() {
		p, err := agent.Parse([]byte(profileText("...\n", "x")), known)
		if err != nil {
			panic(err)
		}
		Expect(p.Name).To(Equal("a"))
	})

	DescribeTable("refuses a hidden character in a single-line field",
		func(field, escape string) {
			text := "---\nname: a\ndescription: d\nmodel: m\n---\nbody\n"
			text = strings.Replace(text, field+": ", field+": \"x"+escape+"\"\n#", 1)
			_, err := agent.Parse([]byte(text), known)
			Expect(err).To(MatchError(ContainSubstring(field + " holds a control, format or separator character")))
		},
		hiddenEntries(),
	)
	It("refuses a description of only a zero-width space", func() {
		_, err := agent.Parse([]byte("---\nname: a\ndescription: \"\\u200b\"\n---\nx\n"), known)
		Expect(err).To(MatchError(ContainSubstring("holds a control, format or separator character")))
	})
	It("accepts a null max_turns as the default", func() {
		p, err := agent.Parse([]byte(profileText("max_turns:\n", "x")), known)
		if err != nil {
			panic(err)
		}
		Expect(p.MaxTurns).To(Equal(25))
	})
	It("accepts a zero-width joiner in the body", func() {
		p, err := agent.Parse([]byte(profileText("", "a\u200db")), known)
		if err != nil {
			panic(err)
		}
		Expect(p.Body).To(Equal("a\u200db"))
	})

	DescribeTable("refuses",
		func(text, reason string) {
			_, err := agent.Parse([]byte(text), known)
			Expect(err).To(MatchError(ContainSubstring(reason)))
		},
		Entry("an unknown field", profileText("colour: red\n", "Do it."), "colour"),
		Entry("a tool that is not registered", profileText("tools: [fs.nuke]\n", "Do it."), `"fs.nuke" is not registered`),
		Entry("a duplicate tool", profileText("tools: [echo, echo]\n", "Do it."), `"echo" is listed twice`),
		Entry("max_turns 0", profileText("max_turns: 0\n", "Do it."), "max_turns 0"),
		Entry("max_turns 101", profileText("max_turns: 101\n", "Do it."), "max_turns 101"),
		Entry("an upper-case name", "---\nname: Bad\ndescription: d\n---\nx\n", "name must match"),
		Entry("a name over 64 characters", "---\nname: a"+strings.Repeat("b", 64)+"\ndescription: d\n---\nx\n", "name must match"),
		Entry("a missing name", "---\ndescription: d\n---\nx\n", "name must match"),
		Entry("a missing description", "---\nname: a\n---\nx\n", "description is required"),
		Entry("an empty body", profileText("", " \n\n"), "body (system prompt) is empty"),
		Entry("frontmatter not at byte 0", "\n"+reviewer, "byte 0"),
		Entry("frontmatter never closed", "---\nname: a\ndescription: d\n", "not closed"),
		Entry("malformed YAML", "---\nname: [\n---\nx\n", "frontmatter"),
		Entry("a float max_turns", profileText("max_turns: 5.5\n", "x"), "plain decimal"),
		Entry("an exponent max_turns", profileText("max_turns: 1e2\n", "x"), "plain decimal"),
		Entry("a hex max_turns", profileText("max_turns: 0x10\n", "x"), "plain decimal"),
		Entry("an octal-looking max_turns", profileText("max_turns: 010\n", "x"), "plain decimal"),
		Entry("a string max_turns", profileText("max_turns: \"5\"\n", "x"), "plain decimal"),
		Entry("a null tools entry", profileText("tools: [echo, null]\n", "x"), "must be a string"),
		Entry("a tilde tools entry", profileText("tools: [~]\n", "x"), "must be a string"),
		Entry("a numeric tools entry", profileText("tools: [5]\n", "x"), "must be a string"),
		Entry("a tagged tools entry", profileText("tools: [!!binary ZWNobw==]\n", "x"), "explicit YAML tag"),
		Entry("a tagged description", "---\nname: a\ndescription: !!str d\n---\nx\n", "explicit YAML tag"),
		Entry("content after an end marker", profileText("...\nname: b\n", "x"), "one YAML document"),
		Entry("a duplicate key", "---\nname: a\nname: b\ndescription: d\n---\nx\n", "twice"),
		Entry("a control character in the description", "---\nname: a\ndescription: \"d\\x01\"\n---\nx\n", "control, format or separator"),
		Entry("a control character in the model", "---\nname: a\ndescription: d\nmodel: \"m\\x07\"\n---\nx\n", "control, format or separator"),
		Entry("a control character in the name", "---\nname: \"a\\x01b\"\ndescription: d\n---\nx\n", "control, format or separator"),
		Entry("invalid UTF-8 in the body", profileText("", "x\xff\xfe"), "valid UTF-8"),
		Entry("a NUL in the body", profileText("", "x\x00y"), "NUL, ESC, DEL or C1"),
		Entry("an ESC in the body", profileText("", "x\x1b[2Jy"), "NUL, ESC, DEL or C1"),
		Entry("a DEL in the body", profileText("", "x\x7fy"), "NUL, ESC, DEL or C1"),
		Entry("a C1 control in the body", profileText("", "x\u009by"), "NUL, ESC, DEL or C1"),
		Entry("a tools scalar", profileText("tools: echo\n", "x"), "tools must be a list"),
		Entry("a blank description", "---\nname: a\ndescription: \"  \"\n---\nx\n", "description is required"),
		Entry("a folded description", "---\nname: a\ndescription: >\n  d\n---\nx\n",
			"description must be a single line: block scalars are not allowed"),
		Entry("a literal description", "---\nname: a\ndescription: |\n  d\n---\nx\n",
			"description must be a single line: block scalars are not allowed"),
		Entry("a folded model", "---\nname: a\ndescription: d\nmodel: >\n  m\n---\nx\n",
			"model must be a single line: block scalars are not allowed"),
		Entry("a close fence with doubled CR", "---\nname: a\ndescription: d\n---\r\r\nx\n", "not closed"),
		Entry("a close fence with trailing text", "---\nname: a\ndescription: d\n--- x\nbody\n", "not closed"),
	)
})

var _ = Describe("LoadFile", func() {
	var dir string

	BeforeEach(func() { dir = GinkgoT().TempDir() })

	It("reports the SHA-256 of the file bytes", func() {
		p, err := agent.LoadFile(put(dir, "r.md", reviewer), agent.SourceUser, known)
		if err != nil {
			panic(err)
		}
		sum := sha256.Sum256([]byte(reviewer))
		Expect(p.SHA256).To(Equal(hex.EncodeToString(sum[:])))
	})
	It("accepts a file of exactly the size cap", func() {
		text := reviewer + strings.Repeat("x", agent.MaxFileBytes-len(reviewer))
		p, err := agent.LoadFile(put(dir, "r.md", text), agent.SourceUser, known)
		if err != nil {
			panic(err)
		}
		Expect(p.Name).To(Equal("reviewer"))
	})
	It("refuses a file over the size cap", func() {
		text := reviewer + strings.Repeat("x", agent.MaxFileBytes)
		_, err := agent.LoadFile(put(dir, "r.md", text), agent.SourceUser, known)
		Expect(err).To(MatchError(ContainSubstring("larger than")))
	})
	It("refuses a directory named like a profile", func() {
		if err := os.Mkdir(filepath.Join(dir, "x.md"), 0o700); err != nil {
			panic(err)
		}
		_, err := agent.LoadFile(filepath.Join(dir, "x.md"), agent.SourceUser, known)
		Expect(err).To(MatchError(ContainSubstring("not a regular file")))
	})
	It("refuses a missing file", func() {
		_, err := agent.LoadFile(filepath.Join(dir, "none.md"), agent.SourceUser, known)
		Expect(err).To(MatchError(agent.ErrInvalidProfile))
	})
	if runtime.GOOS != "windows" {
		sparse := func() string {
			path := filepath.Join(dir, "big.md")
			if err := os.WriteFile(path, []byte(reviewer), 0o600); err != nil {
				panic(err)
			}
			if err := os.Truncate(path, 128<<20); err != nil {
				panic(err)
			}
			return path
		}
		It("reads at most the size cap of a sparse file", func() {
			path := sparse()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			_, _ = agent.LoadFile(path, agent.SourceUser, known)
			runtime.ReadMemStats(&after)
			Expect(after.TotalAlloc - before.TotalAlloc).To(BeNumerically("<", 8<<20))
		})
		It("refuses a sparse file over the size cap", func() {
			_, err := agent.LoadFile(sparse(), agent.SourceUser, known)
			Expect(err).To(MatchError(ContainSubstring("larger than")))
		})
		DescribeTable("refuses a file name with a hidden character",
			func(name string) {
				put(dir, name, reviewer)
				_, err := agent.LoadDir(dir, agent.SourceUser, known)
				Expect(err).To(MatchError(ContainSubstring("file name holds")))
			},
			Entry("U+009B", "a\u009bx.md"),
			Entry("DEL", "a\x7fx.md"),
			Entry("U+202E", "a\u202ex.md"),
		)
		It("refuses a symlinked profile", func() {
			target := put(dir, "real.md", reviewer)
			link := filepath.Join(dir, "link.md")
			if err := os.Symlink(target, link); err != nil {
				panic(err)
			}
			_, err := agent.LoadFile(link, agent.SourceUser, known)
			Expect(err).To(MatchError(ContainSubstring("not a regular file")))
		})
	}
})

var _ = Describe("LoadDir", func() {
	var dir string

	BeforeEach(func() { dir = GinkgoT().TempDir() })

	It("treats a missing directory as empty", func() {
		got, err := agent.LoadDir(filepath.Join(dir, "none"), agent.SourceUser, known)
		if err != nil {
			panic(err)
		}
		Expect(got).To(BeEmpty())
	})
	if runtime.GOOS != "windows" { // Windows does not fail the directory read of a plain file
		It("fails on an unreadable directory", func() {
			file := put(dir, "file", "x")
			_, err := agent.LoadDir(file, agent.SourceUser, known)
			Expect(err).To(MatchError(agent.ErrInvalidProfile))
		})
	}
	It("ignores hidden, non-markdown and nested files", func() {
		put(dir, "a.md", profileText("", "x"))
		put(dir, ".hidden.md", "not a profile")
		put(dir, "notes.txt", "not a profile")
		if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
			panic(err)
		}
		put(filepath.Join(dir, "sub"), "b.md", "not a profile")
		got, err := agent.LoadDir(dir, agent.SourceUser, known)
		if err != nil {
			panic(err)
		}
		Expect(got).To(HaveLen(1))
	})
	It("fails when two files declare one name", func() {
		put(dir, "a.md", profileText("", "x"))
		put(dir, "b.md", profileText("", "y"))
		_, err := agent.LoadDir(dir, agent.SourceUser, known)
		Expect(err).To(MatchError(ContainSubstring(`both declare "a"`)))
	})
	It("fails on a directory named like a profile", func() {
		if err := os.Mkdir(filepath.Join(dir, "x.md"), 0o700); err != nil {
			panic(err)
		}
		_, err := agent.LoadDir(dir, agent.SourceUser, known)
		Expect(err).To(MatchError(ContainSubstring("not a regular file")))
	})
})

var _ = Describe("Load", func() {
	var userDir, projectDir string

	BeforeEach(func() {
		userDir, projectDir = GinkgoT().TempDir(), GinkgoT().TempDir()
		put(userDir, "a.md", profileText("", "user prompt"))
		put(projectDir, "a.md", profileText("", "project prompt"))
		put(projectDir, "b.md", "---\nname: b\ndescription: d\n---\nx\n")
	})

	It("reads only user profiles by default", func() {
		got, err := agent.Load(agent.ScopeUser, userDir, projectDir, known)
		if err != nil {
			panic(err)
		}
		Expect(got).To(HaveLen(1))
	})
	It("does not read the project directory under the user scope", func() {
		put(projectDir, "bad.md", "broken")
		got, err := agent.Load(agent.ScopeUser, userDir, projectDir, known)
		if err != nil {
			panic(err)
		}
		Expect(got[0].Source).To(Equal(agent.SourceUser))
	})
	It("reads only project profiles under the project scope", func() {
		got, err := agent.Load(agent.ScopeProject, userDir, projectDir, known)
		if err != nil {
			panic(err)
		}
		Expect(got).To(HaveLen(2))
	})
	It("refuses a name found in both sources", func() {
		_, err := agent.Load(agent.ScopeBoth, userDir, projectDir, known)
		Expect(err).To(MatchError(ContainSubstring(filepath.Join(userDir, "a.md") + " and " + filepath.Join(projectDir, "a.md"))))
	})
	It("lists both sources when no name conflicts", func() {
		if err := os.Remove(filepath.Join(projectDir, "a.md")); err != nil {
			panic(err)
		}
		got, err := agent.Load(agent.ScopeBoth, userDir, projectDir, known)
		if err != nil {
			panic(err)
		}
		Expect(got).To(HaveLen(2))
	})
	It("loads a name used only in the project source", func() {
		got, err := agent.Load(agent.ScopeProject, userDir, projectDir, known)
		if err != nil {
			panic(err)
		}
		Expect(got[0].Source).To(Equal(agent.SourceProject))
	})
})

var _ = Describe("ParseScope", func() {
	DescribeTable("maps",
		func(in string, want agent.Scope) {
			got, err := agent.ParseScope(in)
			if err != nil {
				panic(err)
			}
			Expect(got).To(Equal(want))
		},
		Entry("empty to user", "", agent.ScopeUser),
		Entry("project", "project", agent.ScopeProject),
		Entry("both", "both", agent.ScopeBoth),
	)
	It("refuses an unknown scope", func() {
		_, err := agent.ParseScope("everything")
		Expect(err).To(MatchError(agent.ErrInvalidScope))
	})
})

var _ = Describe("model field", func() {
	load := func(model string) (agent.Profile, error) {
		path := put(GinkgoT().TempDir(), "a.md", profileText("model: "+model+"\n", "body\n"))
		return agent.LoadFile(path, agent.SourceUser, known)
	}
	It("trims surrounding whitespace", func() {
		p, err := load(`"  gpt-x\t "`)
		if err != nil {
			panic(err)
		}
		Expect(p.Model).To(Equal("gpt-x"))
	})
	It("treats a blank model as inherit", func() {
		p, err := load(`"   "`)
		if err != nil {
			panic(err)
		}
		Expect(p.Model).To(Equal(agent.InheritModel))
	})
	It("accepts a model of exactly the cap", func() {
		p, err := load(strings.Repeat("m", agent.MaxModelChars))
		if err != nil {
			panic(err)
		}
		Expect(p.Model).To(HaveLen(agent.MaxModelChars))
	})
	It("refuses a model over the cap", func() {
		_, err := load(strings.Repeat("m", agent.MaxModelChars+1))
		Expect(err).To(MatchError(ContainSubstring("model is longer than")))
	})
	It("quotes a hidden character in a YAML tag error", func() {
		_, err := load("!<tag:%c2%9b31m> v")
		Expect(err).To(MatchError(ContainSubstring(`\u009b`)))
	})
})
