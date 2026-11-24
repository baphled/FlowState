package app

import (
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
)

// agentText is a valid profile named name.
func agentText(name string) string {
	return "---\nname: " + name + "\ndescription: d\ntools: [fs.read]\n---\nYou review.\n"
}

// putAgent writes a profile file below dir, creating dir.
func putAgent(dir, file, content string) string {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		panic(err)
	}
	path := filepath.Join(dir, file)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		panic(err)
	}
	return path
}

var _ = Describe("Agent profile loading", func() {
	var userDir, workdir string

	BeforeEach(func() {
		config := GinkgoT().TempDir()
		for _, env := range []string{"XDG_CONFIG_HOME", "HOME", "APPDATA"} {
			GinkgoT().Setenv(env, config)
		}
		base, err := os.UserConfigDir()
		if err != nil {
			panic(err)
		}
		userDir = filepath.Join(base, "flowstate", "agents")
		workdir = GinkgoT().TempDir()
	})

	names := func(profiles []agent.Profile) []string {
		out := []string{}
		for _, p := range profiles {
			out = append(out, p.Name+":"+string(p.Source))
		}
		return out
	}

	It("lists the user profiles by default", func() {
		putAgent(userDir, "u.md", agentText("u"))
		got, err := LoadProfiles("", workdir)
		if err != nil {
			panic(err)
		}
		Expect(names(got)).To(Equal([]string{"u:user"}))
	})
	It("does not load project profiles under the default scope", func() {
		putAgent(filepath.Join(workdir, ".flowstate", "agents"), "p.md", agentText("p"))
		got, err := LoadProfiles("user", workdir)
		if err != nil {
			panic(err)
		}
		Expect(got).To(BeEmpty())
	})
	It("loads project profiles on an explicit project scope", func() {
		putAgent(filepath.Join(workdir, ".flowstate", "agents"), "p.md", agentText("p"))
		got, err := LoadProfiles("project", workdir)
		if err != nil {
			panic(err)
		}
		Expect(names(got)).To(Equal([]string{"p:project"}))
	})
	It("refuses a name found in both directories as config_invalid", func() {
		putAgent(userDir, "a.md", agentText("a"))
		putAgent(filepath.Join(workdir, ".flowstate", "agents"), "a.md", agentText("a"))
		_, err := LoadProfiles("both", workdir)
		Expect(err).To(MatchError(ErrConfigInvalid))
	})
	It("lists both directories when no name conflicts", func() {
		putAgent(userDir, "u.md", agentText("u"))
		putAgent(filepath.Join(workdir, ".flowstate", "agents"), "p.md", agentText("p"))
		got, err := LoadProfiles("both", workdir)
		if err != nil {
			panic(err)
		}
		Expect(names(got)).To(Equal([]string{"p:project", "u:user"}))
	})
	It("reports a missing user config directory as config_invalid", func() {
		for _, env := range []string{"XDG_CONFIG_HOME", "HOME", "APPDATA"} {
			GinkgoT().Setenv(env, "")
		}
		_, err := LoadProfiles("user", workdir)
		Expect(err).To(MatchError(ErrConfigInvalid))
	})
	It("returns an empty, non-nil list when nothing is found", func() {
		got, err := LoadProfiles("both", workdir)
		if err != nil {
			panic(err)
		}
		Expect(got).To(Equal([]agent.Profile{}))
	})
	It("refuses an unknown scope as a usage error", func() {
		_, err := LoadProfiles("everything", workdir)
		Expect(err).To(MatchError(ErrUsage))
	})
	It("reports an invalid profile as config_invalid", func() {
		putAgent(userDir, "bad.md", "no frontmatter")
		_, err := LoadProfiles("user", workdir)
		Expect(err).To(MatchError(ErrConfigInvalid))
	})
	It("accepts a profile that uses a registered tool", func() {
		Expect(ValidateAgentFile(putAgent(workdir, "ok.md", agentText("ok")))).To(Succeed())
	})
	It("refuses a profile with an unregistered tool as config_invalid", func() {
		bad := "---\nname: x\ndescription: d\ntools: [fs.nuke]\n---\nbody\n"
		err := ValidateAgentFile(putAgent(workdir, "bad.md", bad))
		Expect(errors.Is(err, ErrConfigInvalid)).To(BeTrue())
	})
})

var _ = Describe("Agent tool names", func() {
	It("include every tool of the session registry", func() {
		registry, err := NewSessionToolRegistry(GinkgoT().TempDir())
		if err != nil {
			panic(err)
		}
		Expect(AgentToolNames()).To(ContainElements(registry.Names()))
	})
	It("include exec.run whether or not its opt-in is set", func() {
		Expect(AgentToolNames()).To(ContainElement("exec.run"))
	})
	It("include the reserved delegate name", func() {
		Expect(AgentToolNames()).To(ContainElement(agent.DelegateTool))
	})
})

var _ = Describe("Agent profile delegates", func() {
	var userDir, workdir string

	BeforeEach(func() {
		config := GinkgoT().TempDir()
		for _, env := range []string{"XDG_CONFIG_HOME", "HOME", "APPDATA"} {
			GinkgoT().Setenv(env, config)
		}
		base, err := os.UserConfigDir()
		if err != nil {
			panic(err)
		}
		userDir = filepath.Join(base, "flowstate", "agents")
		workdir = GinkgoT().TempDir()
	})

	lead := func(delegates string) string {
		return "---\nname: lead\ndescription: d\ntools: [delegate]\ndelegates: " + delegates + "\n---\nLead.\n"
	}

	It("loads a delegate that is in the loaded set", func() {
		putAgent(userDir, "lead.md", lead("[eng]"))
		putAgent(userDir, "eng.md", agentText("eng"))
		got, err := LoadProfiles("user", workdir)
		if err != nil {
			panic(err)
		}
		Expect(got[1].Delegates).To(Equal([]string{"eng"}))
	})
	It("refuses an unknown delegate as config_invalid", func() {
		putAgent(userDir, "lead.md", lead("[ghost]"))
		_, err := LoadProfiles("user", workdir)
		Expect(err).To(MatchError(ErrConfigInvalid))
	})
	It("names the profile and the unknown delegate", func() {
		putAgent(userDir, "lead.md", lead("[ghost]"))
		_, err := LoadProfiles("user", workdir)
		Expect(err).To(MatchError(ContainSubstring(`profile "lead" delegates to "ghost"`)))
	})
	It("refuses a delegate that lives only in a scope that is not loaded", func() {
		putAgent(userDir, "lead.md", lead("[eng]"))
		putAgent(filepath.Join(workdir, ".flowstate", "agents"), "eng.md", agentText("eng"))
		_, err := LoadProfiles("user", workdir)
		Expect(err).To(MatchError(ContainSubstring(`"eng"`)))
	})
	It("loads a delegate found in the other scope under both", func() {
		putAgent(userDir, "lead.md", lead("[eng]"))
		putAgent(filepath.Join(workdir, ".flowstate", "agents"), "eng.md", agentText("eng"))
		got, err := LoadProfiles("both", workdir)
		if err != nil {
			panic(err)
		}
		Expect(got).To(HaveLen(2))
	})
	It("loads a project lead that delegates to a user profile under both", func() {
		putAgent(filepath.Join(workdir, ".flowstate", "agents"), "lead.md", lead("[eng]"))
		putAgent(userDir, "eng.md", agentText("eng"))
		got, err := LoadProfiles("both", workdir)
		if err != nil {
			panic(err)
		}
		Expect(got[1].Delegates).To(Equal([]string{"eng"}))
	})
	It("loads a cycle", func() {
		putAgent(userDir, "a.md", "---\nname: a\ndescription: d\ntools: [delegate]\ndelegates: [b]\n---\nA.\n")
		putAgent(userDir, "b.md", "---\nname: b\ndescription: d\ntools: [delegate]\ndelegates: [a]\n---\nB.\n")
		got, err := LoadProfiles("user", workdir)
		if err != nil {
			panic(err)
		}
		Expect(got).To(HaveLen(2))
	})
	It("loads delegate in tools without delegates as an empty list", func() {
		putAgent(userDir, "lead.md", "---\nname: lead\ndescription: d\ntools: [delegate]\n---\nLead.\n")
		got, err := LoadProfiles("user", workdir)
		if err != nil {
			panic(err)
		}
		Expect(got[0].Delegates).To(Equal([]string{}))
	})
})
