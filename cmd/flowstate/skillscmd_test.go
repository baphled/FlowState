package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func skillDoc(name, extra string) string {
	return "---\nname: " + name + "\ndescription: About " + name + "\n" + extra + "---\nBody.\n"
}

func writeSkill(dir, name, content string) {
	path := filepath.Join(dir, name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		panic(err)
	}
}

func skillsOut(args ...string) (result, map[string]any) {
	r := runArgv(append([]string{"skills"}, args...))
	var out map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		panic(err)
	}
	return r, out
}

var _ = Describe("Skills CLI", func() {
	var userDir, dir string

	BeforeEach(func() {
		agentsDir, _ := agentsFixture()
		userDir = filepath.Join(filepath.Dir(agentsDir), "skills")
		dir = GinkgoT().TempDir()
	})

	It("lists the skills of a directory", func() {
		writeSkill(dir, "tdd", skillDoc("tdd", ""))
		_, out := skillsOut("list", dir)
		Expect(out["skills"]).To(HaveLen(1))
	})
	It("lists the user directory when none is named", func() {
		writeSkill(userDir, "tdd", skillDoc("tdd", ""))
		_, out := skillsOut("list")
		Expect(out["skills"].([]any)[0].(map[string]any)["source"]).To(Equal("user"))
	})
	It("prints no hash with validate", func() {
		writeSkill(dir, "tdd", skillDoc("tdd", ""))
		r, _ := skillsOut("validate", dir)
		Expect(strings.ToLower(r.stdout)).NotTo(MatchRegexp("[0-9a-f]{64}"))
	})
	It("does not read the user directory when a directory is named", func() {
		writeSkill(userDir, "tdd", skillDoc("tdd", ""))
		_, out := skillsOut("list", dir)
		Expect(out["skills"]).To(BeEmpty())
	})
	It("counts roots, loaded skills, errors and warnings", func() {
		writeSkill(dir, "tdd", skillDoc("tdd", "tier: 1\n"))
		writeSkill(dir, "bad", "nothing")
		_, out := skillsOut("validate", dir)
		Expect(out["counts"]).To(Equal(map[string]any{"roots": 1.0, "loaded": 1.0, "errors": 1.0, "warnings": 1.0}))
	})
	It("exits 0 from list even when a skill is invalid", func() {
		writeSkill(dir, "bad", "nothing")
		Expect(runArgv([]string{"skills", "list", dir}).exit).To(Equal(0))
	})
	It("shows the error count in list", func() {
		writeSkill(dir, "bad", "nothing")
		_, out := skillsOut("list", dir)
		Expect(out["counts"].(map[string]any)["errors"]).To(BeEquivalentTo(1))
	})
	It("exits 4 from validate when a skill is invalid", func() {
		writeSkill(dir, "bad", "nothing")
		r, _ := skillsOut("validate", dir)
		Expect(r.exit).To(Equal(4))
	})
	It("exits 0 from validate for an unknown key", func() {
		writeSkill(dir, "tdd", skillDoc("tdd", "category: x\n"))
		Expect(runArgv([]string{"skills", "validate", dir}).exit).To(Equal(0))
	})
	It("prints the warning for an unknown key in validate", func() {
		writeSkill(dir, "tdd", skillDoc("tdd", "category: x\n"))
		r, _ := skillsOut("validate", dir)
		Expect(r.stdout).To(ContainSubstring("ignored, not supported: category"))
	})
	It("prints the diagnostic of an invalid skill in validate", func() {
		writeSkill(dir, "bad", "nothing")
		r, _ := skillsOut("validate", dir)
		Expect(r.stdout).To(ContainSubstring("frontmatter must start with"))
	})
	It("exits 4 from validate for a duplicate across directories", func() {
		other := GinkgoT().TempDir()
		writeSkill(dir, "tdd", skillDoc("tdd", ""))
		writeSkill(other, "tdd", skillDoc("tdd", ""))
		r, _ := skillsOut("validate", dir, other)
		Expect(r.exit).To(Equal(4))
	})
	It("accepts and ignores --json", func() {
		writeSkill(dir, "tdd", skillDoc("tdd", ""))
		_, out := skillsOut("list", "--json", dir)
		Expect(out["skills"]).To(HaveLen(1))
	})
	It("refuses a missing subcommand with the usage exit", func() {
		Expect(runArgv([]string{"skills"}).exit).To(Equal(2))
	})
	It("refuses an unknown subcommand with the usage exit", func() {
		Expect(runArgv([]string{"skills", "install"}).exit).To(Equal(2))
	})
	It("refuses a flag-like argument with the usage exit", func() {
		Expect(runArgv([]string{"skills", "list", "--skills-path"}).exit).To(Equal(2))
	})
})
