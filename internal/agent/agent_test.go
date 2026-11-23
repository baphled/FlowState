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
	It("lets a project profile override a user profile of the same name", func() {
		got, err := agent.Load(agent.ScopeBoth, userDir, projectDir, known)
		if err != nil {
			panic(err)
		}
		Expect(got[0].Source).To(Equal(agent.SourceProject))
	})
	It("reports the override", func() {
		got, err := agent.Load(agent.ScopeBoth, userDir, projectDir, known)
		if err != nil {
			panic(err)
		}
		Expect(got[0].Overrides).To(Equal(agent.SourceUser))
	})
	It("reports no override for a profile that replaced nothing", func() {
		got, err := agent.Load(agent.ScopeBoth, userDir, projectDir, known)
		if err != nil {
			panic(err)
		}
		Expect(got[1].Overrides).To(BeEmpty())
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
