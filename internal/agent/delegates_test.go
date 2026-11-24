package agent_test

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
)

// delegating builds a profile named a whose tools hold delegate and whose
// delegates line is the given YAML (with its newline).
func delegating(delegates string) string {
	return profileText("tools: [delegate, fs.read]\n"+delegates, "Do it.")
}

var _ = Describe("delegates field", func() {
	It("reads the listed profile names in order", func() {
		p, err := agent.Parse([]byte(delegating("delegates: [engineer, reviewer]\n")), known)
		if err != nil {
			panic(err)
		}
		Expect(p.Delegates).To(Equal([]string{"engineer", "reviewer"}))
	})
	It("loads delegate in tools without delegates as an empty list", func() {
		p, err := agent.Parse([]byte(delegating("")), known)
		if err != nil {
			panic(err)
		}
		Expect(p.Delegates).To(Equal([]string{}))
	})
	It("reads a null delegates as an empty list", func() {
		p, err := agent.Parse([]byte(delegating("delegates:\n")), known)
		if err != nil {
			panic(err)
		}
		Expect(p.Delegates).To(Equal([]string{}))
	})
	It("accepts the largest list", func() {
		names := make([]string, agent.MaxDelegates)
		for i := range names {
			names[i] = "p" + strings.Repeat("x", i)
		}
		p, err := agent.Parse([]byte(delegating("delegates: ["+strings.Join(names, ", ")+"]\n")), known)
		if err != nil {
			panic(err)
		}
		Expect(p.Delegates).To(HaveLen(agent.MaxDelegates))
	})

	DescribeTable("refuses",
		func(text, reason string) {
			_, err := agent.Parse([]byte(text), known)
			Expect(err).To(MatchError(ContainSubstring(reason)))
		},
		Entry("delegates without delegate in tools",
			profileText("tools: [fs.read]\ndelegates: [engineer]\n", "x"), "delegates needs delegate in tools"),
		Entry("delegates with no tools at all",
			profileText("delegates: [engineer]\n", "x"), "delegates needs delegate in tools"),
		Entry("an upper-case name", delegating("delegates: [Engineer]\n"), "must match"),
		Entry("a name with a dot", delegating("delegates: [a.b]\n"), "must match"),
		Entry("a duplicate", delegating("delegates: [engineer, engineer]\n"), `lists "engineer" twice`),
		Entry("the profile itself", delegating("delegates: [a]\n"), "the profile itself"),
		Entry("a scalar", delegating("delegates: engineer\n"), "delegates must be a list"),
		Entry("a null entry", delegating("delegates: [engineer, null]\n"), "must be a string"),
		Entry("a lookalike name, shown escaped", delegating("delegates: [\u0435ng]\n"), `\u0435ng`),
		Entry("a numeric entry", delegating("delegates: [5]\n"), "must be a string"),
		Entry("a tagged entry", delegating("delegates: [!!str engineer]\n"), "explicit YAML tag"),
		Entry("33 entries", delegating("delegates: ["+manyNames(agent.MaxDelegates+1)+"]\n"), "more than 32"),
	)

	It("changes the file digest when delegates changes", func() {
		dir := GinkgoT().TempDir()
		one := put(dir, "one.md", delegating("delegates: [engineer]\n"))
		two := put(dir, "two.md", delegating("delegates: [reviewer]\n"))
		p1, err := agent.LoadFile(one, agent.SourceUser, known)
		if err != nil {
			panic(err)
		}
		p2, err := agent.LoadFile(two, agent.SourceUser, known)
		if err != nil {
			panic(err)
		}
		Expect(p1.SHA256).NotTo(Equal(p2.SHA256))
	})
	It("covers the delegates bytes in the file digest", func() {
		text := delegating("delegates: [engineer]\n")
		p, err := agent.LoadFile(put(GinkgoT().TempDir(), "a.md", text), agent.SourceUser, known)
		if err != nil {
			panic(err)
		}
		sum := sha256.Sum256([]byte(text))
		Expect(p.SHA256).To(Equal(hex.EncodeToString(sum[:])))
	})
})

// manyNames returns n distinct profile names joined by ", ".
func manyNames(n int) string {
	names := make([]string, n)
	for i := range names {
		names[i] = "p" + strings.Repeat("x", i)
	}
	return strings.Join(names, ", ")
}

var _ = Describe("CheckDelegates", func() {
	It("accepts a set where every delegate is loaded", func() {
		set := []agent.Profile{{Name: "lead", Delegates: []string{"eng"}}, {Name: "eng"}}
		Expect(agent.CheckDelegates(set)).To(Succeed())
	})
	It("accepts an empty set", func() {
		Expect(agent.CheckDelegates(nil)).To(Succeed())
	})
	It("refuses an unknown delegate naming the profile", func() {
		err := agent.CheckDelegates([]agent.Profile{{Name: "lead", Delegates: []string{"ghost"}}})
		Expect(err).To(MatchError(ContainSubstring(`profile "lead" delegates to "ghost"`)))
	})
	It("marks the refusal as an invalid profile", func() {
		err := agent.CheckDelegates([]agent.Profile{{Name: "lead", Delegates: []string{"ghost"}}})
		Expect(err).To(MatchError(agent.ErrInvalidProfile))
	})
	It("refuses a hand-built profile that delegates to itself", func() {
		err := agent.CheckDelegates([]agent.Profile{{Name: "a", Delegates: []string{"a"}}})
		Expect(err).To(MatchError(ContainSubstring("the profile itself")))
	})
	It("refuses a hand-built profile with more than 32 delegates", func() {
		names := make([]string, agent.MaxDelegates+1)
		set := make([]agent.Profile, 0, len(names)+1)
		for i := range names {
			names[i] = "p" + strings.Repeat("x", i)
			set = append(set, agent.Profile{Name: names[i]})
		}
		set = append(set, agent.Profile{Name: "lead", Delegates: names})
		Expect(agent.CheckDelegates(set)).To(MatchError(ContainSubstring("more than 32")))
	})
	It("allows a cycle", func() {
		set := []agent.Profile{{Name: "a", Delegates: []string{"b"}}, {Name: "b", Delegates: []string{"a"}}}
		Expect(agent.CheckDelegates(set)).To(Succeed())
	})
})

var _ = Describe("Load with delegates", func() {
	It("does not cross-check inside LoadDir", func() {
		dir := GinkgoT().TempDir()
		put(dir, "a.md", delegating("delegates: [ghost]\n"))
		got, err := agent.LoadDir(dir, agent.SourceUser, known)
		if err != nil {
			panic(err)
		}
		Expect(got[0].Delegates).To(Equal([]string{"ghost"}))
	})
	It("keeps the path of the file that declared the delegates", func() {
		dir := GinkgoT().TempDir()
		path := put(dir, "a.md", delegating("delegates: [b]\n"))
		p, err := agent.LoadFile(path, agent.SourceUser, known)
		if err != nil {
			panic(err)
		}
		Expect(filepath.Base(p.Path)).To(Equal("a.md"))
	})
})
