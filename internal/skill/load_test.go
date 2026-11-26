package skill_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/skill"
)

func put(dir, rel, content string) string {
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		panic(err)
	}
	return path
}

func named(name, extra string) string {
	return "---\nname: " + name + "\ndescription: About " + name + "\n" + extra + "---\nBody of " + name + ".\n"
}

var _ = Describe("Load", func() {
	var root, other string

	BeforeEach(func() {
		root, other = GinkgoT().TempDir(), GinkgoT().TempDir()
	})

	load := func(dirs ...string) skill.Result {
		roots := make([]skill.Root, len(dirs))
		for i, d := range dirs {
			roots[i] = skill.Root{Dir: d, Source: skill.SourcePath}
		}
		return skill.Load(roots)
	}
	errorsOf := func(r skill.Result) []string {
		out := []string{}
		for _, d := range r.Diagnostics {
			if d.Severity == skill.SeverityError {
				out = append(out, d.Message)
			}
		}
		return out
	}
	warningsOf := func(r skill.Result) []string {
		out := []string{}
		for _, d := range r.Diagnostics {
			if d.Severity == skill.SeverityWarning {
				out = append(out, d.Message)
			}
		}
		return out
	}
	names := func(r skill.Result) []string {
		out := []string{}
		for _, s := range r.Skills {
			out = append(out, s.Name)
		}
		return out
	}

	It("loads skills sorted by name", func() {
		put(root, "tdd/SKILL.md", named("tdd", ""))
		put(root, "code-review/SKILL.md", named("code-review", ""))
		Expect(names(load(root))).To(Equal([]string{"code-review", "tdd"}))
	})
	It("records the source and the SKILL.md path", func() {
		path := put(root, "tdd/SKILL.md", named("tdd", ""))
		s := load(root).Skills[0]
		Expect([]string{string(s.Source), s.Path}).To(Equal([]string{"path", path}))
	})
	It("treats a missing root as empty", func() {
		Expect(load(filepath.Join(root, "absent")).Diagnostics).To(BeEmpty())
	})
	It("ignores files and hidden directories", func() {
		put(root, "README.md", "x")
		put(root, ".git/SKILL.md", named("git", ""))
		Expect(load(root).Diagnostics).To(BeEmpty())
	})
	It("does not load a nested skill", func() {
		put(root, "group/tdd/SKILL.md", named("tdd", ""))
		Expect(load(root).Skills).To(BeEmpty())
	})
	It("names both the declared name and the directory on a mismatch", func() {
		put(root, "tdd/SKILL.md", named("other", ""))
		Expect(errorsOf(load(root))).To(ConsistOf(ContainSubstring(`name "other" does not match its directory "tdd"`)))
	})
	It("names both paths of a duplicate", func() {
		first := put(root, "tdd/SKILL.md", named("tdd", ""))
		second := put(other, "tdd/SKILL.md", named("tdd", ""))
		Expect(errorsOf(load(root, other))).To(ConsistOf(And(ContainSubstring(first), ContainSubstring(second))))
	})
	It("warns once per skill naming the ignored keys", func() {
		put(root, "tdd/SKILL.md", named("tdd", "tier: 2\ncategory: x\n"))
		Expect(warningsOf(load(root))).To(ConsistOf("ignored, not supported: category, tier"))
	})
	It("does not let a bad skill block the others", func() {
		put(root, "bad/SKILL.md", "no frontmatter")
		put(root, "tdd/SKILL.md", named("tdd", ""))
		Expect(names(load(root))).To(Equal([]string{"tdd"}))
	})
	It("reports a directory without a SKILL.md", func() {
		put(root, "empty/notes.md", "x")
		Expect(errorsOf(load(root))).To(ConsistOf("SKILL.md is missing"))
	})
	It("warns about a supporting file over the cap", func() {
		put(root, "tdd/SKILL.md", named("tdd", ""))
		put(root, "tdd/refs/big.md", strings.Repeat("a", skill.MaxFileBytes+1))
		Expect(warningsOf(load(root))).To(ConsistOf(ContainSubstring("larger than")))
	})
	It("warns about a supporting file whose path skill.load would refuse", func() {
		put(root, "tdd/SKILL.md", named("tdd", ""))
		put(root, "tdd/.secret", "x")
		Expect(warningsOf(load(root))).To(ConsistOf(ContainSubstring("outside the allowed characters")))
	})
	It("accepts CRLF line endings", func() {
		put(root, "tdd/SKILL.md", strings.ReplaceAll(named("tdd", ""), "\n", "\r\n"))
		Expect(names(load(root))).To(Equal([]string{"tdd"}))
	})

	if runtime.GOOS != "windows" {
		mkfifo := func(path string) {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				panic(err)
			}
			if err := exec.Command("mkfifo", path).Run(); err != nil {
				panic(err)
			}
		}
		loadBounded := func(dir string) skill.Result {
			done := make(chan skill.Result, 1)
			go func() { done <- load(dir) }()
			select {
			case res := <-done:
				return res
			case <-time.After(5 * time.Second):
				panic("loader hung on a FIFO")
			}
		}
		fifoSkill := func(d string) string {
			put(d, "good/SKILL.md", named("good", ""))
			mkfifo(filepath.Join(d, "bad", "SKILL.md"))
			return d
		}
		fifoLink := func(d string) string {
			put(d, "good/SKILL.md", named("good", ""))
			mkfifo(filepath.Join(d, "pipe"))
			if err := os.MkdirAll(filepath.Join(d, "bad"), 0o700); err != nil {
				panic(err)
			}
			if err := os.Symlink(filepath.Join("..", "pipe"), filepath.Join(d, "bad", "SKILL.md")); err != nil {
				panic(err)
			}
			return d
		}
		fifoDir := func(d string) string {
			put(d, "good/SKILL.md", named("good", ""))
			mkfifo(filepath.Join(d, "bad"))
			return d
		}
		fifoRoot := func(d string) string { p := filepath.Join(d, "fifo-root"); mkfifo(p); return p }
		DescribeTable("returns promptly on a FIFO",
			func(mk func(d string) string) {
				Expect(loadBounded(mk(root))).NotTo(BeNil())
			},
			Entry("a FIFO SKILL.md", fifoSkill),
			Entry("a symlink to a FIFO SKILL.md", fifoLink),
			Entry("a FIFO as a skill directory name", fifoDir),
			Entry("a FIFO as the root", fifoRoot),
		)
		DescribeTable("keeps the other skills beside a FIFO",
			func(mk func(d string) string) {
				Expect(names(loadBounded(mk(root)))).To(Equal([]string{"good"}))
			},
			Entry("a FIFO SKILL.md", fifoSkill),
			Entry("a symlink to a FIFO SKILL.md", fifoLink),
			Entry("a FIFO as a skill directory name", fifoDir),
		)
		DescribeTable("reports an invalid skill for a FIFO",
			func(mk func(d string) string) {
				Expect(errorsOf(loadBounded(mk(root)))).NotTo(BeEmpty())
			},
			Entry("a FIFO SKILL.md", fifoSkill),
			Entry("a symlink to a FIFO SKILL.md", fifoLink),
			Entry("a FIFO as the root", fifoRoot),
		)
		It("does not load a skill directory that is a symlink out of the root", func() {
			put(other, "SKILL.md", named("tdd", ""))
			if err := os.Symlink(other, filepath.Join(root, "tdd")); err != nil {
				panic(err)
			}
			Expect(load(root).Skills).To(BeEmpty())
		})
		It("does not load a SKILL.md that is a symlink out of the root", func() {
			outside := put(other, "SKILL.md", named("tdd", ""))
			if err := os.MkdirAll(filepath.Join(root, "tdd"), 0o700); err != nil {
				panic(err)
			}
			if err := os.Symlink(outside, filepath.Join(root, "tdd", "SKILL.md")); err != nil {
				panic(err)
			}
			Expect(load(root).Skills).To(BeEmpty())
		})
		It("loads a SKILL.md that is a symlink staying inside the root", func() {
			put(root, "shared/SKILL.md", named("tdd", ""))
			if err := os.MkdirAll(filepath.Join(root, "tdd"), 0o700); err != nil {
				panic(err)
			}
			if err := os.Symlink(filepath.Join("..", "shared", "SKILL.md"), filepath.Join(root, "tdd", "SKILL.md")); err != nil {
				panic(err)
			}
			Expect(names(load(root))).To(ContainElement("tdd"))
		})
		It("loads a skill directory that is a symlink staying inside the root", func() {
			put(root, "real/SKILL.md", named("alias", ""))
			if err := os.Symlink("real", filepath.Join(root, "alias")); err != nil {
				panic(err)
			}
			Expect(names(load(root))).To(ContainElement("alias"))
		})
		It("reports a root that is not a directory", func() {
			file := put(root, "file", "x")
			Expect(errorsOf(load(file))).To(ConsistOf(ContainSubstring("cannot open the skills directory")))
		})
		It("does not follow a symlinked supporting directory", func() {
			put(root, "tdd/SKILL.md", named("tdd", ""))
			put(other, "big.md", strings.Repeat("a", skill.MaxFileBytes+1))
			if err := os.Symlink(other, filepath.Join(root, "tdd", "refs")); err != nil {
				panic(err)
			}
			Expect(warningsOf(load(root))).To(BeEmpty())
		})
	}
})
