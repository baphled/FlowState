package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const delegatingProfile = "---\nname: lead\ndescription: d\ntools: [delegate]\ndelegates: [eng]\n---\nLead.\n"

const agentProfile = "---\nname: reviewer\ndescription: Reads a change\ntools: [fs.read]\n---\nYou review.\n"

// agentsFixture isolates the user config directory and returns it with an
// empty working directory.
func agentsFixture() (userDir, workdir string) {
	config := GinkgoT().TempDir()
	for _, env := range []string{"XDG_CONFIG_HOME", "HOME", "APPDATA"} {
		GinkgoT().Setenv(env, config)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		panic(err)
	}
	return filepath.Join(base, "flowstate", "agents"), GinkgoT().TempDir()
}

// writeAgent writes a profile file, creating its directory, and returns the path.
func writeAgent(dir, name, content string) string {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		panic(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		panic(err)
	}
	return path
}

// listAgents runs `agents list` and decodes its JSON array.
func listAgents(args ...string) (result, []map[string]any) {
	r := runArgv(append([]string{"agents", "list"}, args...))
	var rows []map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &rows); err != nil {
		panic(err)
	}
	return r, rows
}

var _ = Describe("Agents CLI", func() {
	var userDir, workdir string

	BeforeEach(func() { userDir, workdir = agentsFixture() })

	It("lists an empty array when there are no profiles", func() {
		r := runArgv([]string{"agents", "list", "--workdir", workdir})
		Expect(r.stdout).To(Equal("[]\n"))
	})
	It("lists a user profile", func() {
		writeAgent(userDir, "reviewer.md", agentProfile)
		_, rows := listAgents("--workdir", workdir)
		Expect(rows[0]["name"]).To(Equal("reviewer"))
	})
	It("prints the SHA-256 of the file", func() {
		writeAgent(userDir, "reviewer.md", agentProfile)
		sum := sha256.Sum256([]byte(agentProfile))
		_, rows := listAgents("--workdir", workdir)
		Expect(rows[0]["sha256"]).To(Equal(hex.EncodeToString(sum[:])))
	})
	It("lists delegates as an empty array when none is declared", func() {
		writeAgent(userDir, "reviewer.md", agentProfile)
		_, rows := listAgents("--workdir", workdir)
		Expect(rows[0]["delegates"]).To(Equal([]any{}))
	})
	It("lists the declared delegates", func() {
		writeAgent(userDir, "lead.md", delegatingProfile)
		writeAgent(userDir, "eng.md", "---\nname: eng\ndescription: d\n---\nEng.\n")
		_, rows := listAgents("--workdir", workdir)
		Expect(rows[1]["delegates"]).To(Equal([]any{"eng"}))
	})
	It("conforms to the agents-list schema with delegates", func() {
		writeAgent(userDir, "lead.md", delegatingProfile)
		writeAgent(userDir, "eng.md", "---\nname: eng\ndescription: d\n---\nEng.\n")
		r, _ := listAgents("--workdir", workdir)
		Expect(validateAgainst("agents-list", r.stdout)).To(Succeed())
	})
	It("exits 4 for a delegate that is not loaded", func() {
		writeAgent(userDir, "lead.md", delegatingProfile)
		r := runArgv([]string{"agents", "list", "--workdir", workdir})
		Expect(r.exit).To(Equal(4))
	})
	It("validates a file whose delegate is not checked across files", func() {
		r := runArgv([]string{"agents", "validate", writeAgent(workdir, "lead.md", delegatingProfile)})
		Expect(r.exit).To(Equal(0))
	})
	It("refuses delegates without delegate in tools on validate", func() {
		bad := writeAgent(workdir, "bad.md", "---\nname: x\ndescription: d\ndelegates: [eng]\n---\nbody\n")
		r := runArgv([]string{"agents", "validate", bad})
		Expect(r.stdout).To(ContainSubstring("delegates needs delegate in tools"))
	})
	It("conforms to the agents-list schema", func() {
		writeAgent(userDir, "reviewer.md", agentProfile)
		r, _ := listAgents("--workdir", workdir)
		Expect(validateAgainst("agents-list", r.stdout)).To(Succeed())
	})
	It("does not list project profiles by default", func() {
		writeAgent(filepath.Join(workdir, ".flowstate", "agents"), "p.md", agentProfile)
		r := runArgv([]string{"agents", "list", "--workdir", workdir})
		Expect(r.stdout).To(Equal("[]\n"))
	})
	It("lists a project profile with --agents project", func() {
		writeAgent(filepath.Join(workdir, ".flowstate", "agents"), "p.md", agentProfile)
		_, rows := listAgents("--agents=project", "--workdir", workdir)
		Expect(rows[0]["source"]).To(Equal("project"))
	})
	It("exits 4 for a name found in both sources with --agents both", func() {
		writeAgent(userDir, "reviewer.md", agentProfile)
		writeAgent(filepath.Join(workdir, ".flowstate", "agents"), "p.md", agentProfile)
		r := runArgv([]string{"agents", "list", "--agents", "both", "--workdir", workdir})
		Expect(r.exit).To(Equal(4))
	})
	It("accepts and ignores --json on list", func() {
		writeAgent(userDir, "reviewer.md", agentProfile)
		_, rows := listAgents("--json", "--workdir", workdir)
		Expect(rows[0]["name"]).To(Equal("reviewer"))
	})
	It("refuses an unknown scope with the usage exit", func() {
		r := runArgv([]string{"agents", "list", "--agents", "all"})
		Expect(r.exit).To(Equal(2))
	})
	It("refuses an unknown flag with the usage exit", func() {
		r := runArgv([]string{"agents", "list", "--bogus"})
		Expect(r.exit).To(Equal(2))
	})
	DescribeTable("refuses a near-miss of --json with the usage exit",
		func(flag string) {
			r := runArgv([]string{"agents", "list", flag})
			Expect(r.exit).To(Equal(2))
		},
		Entry("--jsonx", "--jsonx"),
		Entry("--json=true", "--json=true"),
	)
	It("refuses a flag without a value with the usage exit", func() {
		r := runArgv([]string{"agents", "list", "--workdir"})
		Expect(r.exit).To(Equal(2))
	})
	It("exits 4 when a profile is invalid", func() {
		writeAgent(userDir, "bad.md", "no frontmatter")
		r := runArgv([]string{"agents", "list", "--workdir", workdir})
		Expect(r.exit).To(Equal(4))
	})
	It("refuses a missing subcommand with the usage exit", func() {
		r := runArgv([]string{"agents"})
		Expect(r.exit).To(Equal(2))
	})

	Describe("validate", func() {
		It("exits 2 with no files", func() {
			r := runArgv([]string{"agents", "validate"})
			Expect(r.exit).To(Equal(2))
		})
		It("prints ok for a valid file", func() {
			path := writeAgent(workdir, "reviewer.md", agentProfile)
			r := runArgv([]string{"agents", "validate", path})
			Expect(r.stdout).To(Equal(`{"ok":true,"path":` + quoted(path) + "}\n"))
		})
		It("accepts and ignores --json on validate", func() {
			path := writeAgent(workdir, "reviewer.md", agentProfile)
			r := runArgv([]string{"agents", "validate", "--json", path})
			Expect(r.stdout).To(Equal(`{"ok":true,"path":` + quoted(path) + "}\n"))
		})
		DescribeTable("refuses an unknown flag with the usage exit",
			func(flag string) {
				r := runArgv([]string{"agents", "validate", flag, "x"})
				Expect(r.exit).To(Equal(2))
			},
			Entry("--bogus", "--bogus"),
			Entry("--jsonx", "--jsonx"),
			Entry("--json=true", "--json=true"),
		)
		It("conforms to the agents-validate schema", func() {
			r := runArgv([]string{"agents", "validate", writeAgent(workdir, "reviewer.md", agentProfile)})
			Expect(validateAgainst("agents-validate", firstLine(r.stdout))).To(Succeed())
		})
		It("prints no raw C1 byte for a tag holding U+009B", func() {
			bad := writeAgent(workdir, "bad.md", "---\nname: a\ndescription: d\nmodel: !<tag:%c2%9b31m> v\n---\nbody\n")
			r := runArgv([]string{"agents", "validate", bad})
			Expect(r.stdout + r.stderr).NotTo(ContainSubstring("\u009b"))
		})
		It("exits 0 for a valid file", func() {
			r := runArgv([]string{"agents", "validate", writeAgent(workdir, "reviewer.md", agentProfile)})
			Expect(r.exit).To(Equal(0))
		})
		It("exits 4 when any file is invalid", func() {
			good := writeAgent(workdir, "good.md", agentProfile)
			bad := writeAgent(workdir, "bad.md", "---\nname: x\n---\nbody\n")
			r := runArgv([]string{"agents", "validate", good, bad})
			Expect(r.exit).To(Equal(4))
		})
		It("reports the failing file's error", func() {
			bad := writeAgent(workdir, "bad.md", "---\nname: x\n---\nbody\n")
			r := runArgv([]string{"agents", "validate", bad})
			Expect(r.stdout).To(ContainSubstring("description is required"))
		})
		It("still validates the files after a failing one", func() {
			bad := writeAgent(workdir, "bad.md", "nope")
			good := writeAgent(workdir, "good.md", agentProfile)
			r := runArgv([]string{"agents", "validate", bad, good})
			Expect(lines(r.stdout)).To(HaveLen(2))
		})
	})
})

// quoted is the JSON encoding of s.
func quoted(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
