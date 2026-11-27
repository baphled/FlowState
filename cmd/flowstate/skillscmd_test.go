package main

import (
	"encoding/json"
	"fmt"
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

// skillNames lists the names in a skills report, in its order.
func skillNames(out map[string]any) []any {
	names := []any{}
	for _, s := range out["skills"].([]any) {
		names = append(names, s.(map[string]any)["name"])
	}
	return names
}

var _ = Describe("The --skills scope on skills list", func() {
	var project string

	BeforeEach(func() {
		agentsDir, _ := agentsFixture()
		writeSkill(filepath.Join(filepath.Dir(agentsDir), "skills"), "user-skill", skillDoc("user-skill", ""))
		project = inTempDir() // the project root is the process working directory, as for hooks
		writeSkill(filepath.Join(project, ".flowstate", "skills"), "project-skill", skillDoc("project-skill", ""))
	})

	It("does not load a project skill under the default scope", func() {
		_, out := skillsOut("list")
		Expect(skillNames(out)).To(Equal([]any{"user-skill"}))
	})
	It("loads the project skill, and not the user skill, with --skills project", func() {
		_, out := skillsOut("list", "--skills", "project")
		Expect(skillNames(out)).To(Equal([]any{"project-skill"}))
	})
	It("marks a project skill's source as project", func() {
		_, out := skillsOut("list", "--skills", "project")
		Expect(out["skills"].([]any)[0].(map[string]any)["source"]).To(Equal("project"))
	})
	It("loads both directories with --skills both", func() {
		_, out := skillsOut("list", "--skills=both")
		Expect(skillNames(out)).To(Equal([]any{"project-skill", "user-skill"}))
	})
	It("loads nothing with --skills none", func() {
		_, out := skillsOut("list", "--skills", "none")
		Expect(out["counts"]).To(Equal(map[string]any{"roots": 0.0, "loaded": 0.0, "errors": 0.0, "warnings": 0.0}))
	})
	It("refuses --workdir, so the project root can never follow it", func() {
		Expect(runArgv([]string{"skills", "list", "--workdir", project}).exit).To(Equal(2))
	})
	It("exits 2 for an unknown scope", func() {
		Expect(runArgv([]string{"skills", "list", "--skills", "all"}).exit).To(Equal(2))
	})
	It("names the values for an unknown scope", func() {
		r := runArgv([]string{"skills", "list", "--skills", "all"})
		Expect(r.stderr).To(ContainSubstring("user, project, both or none"))
	})
	It("exits 2 for an empty scope", func() {
		Expect(runArgv([]string{"skills", "list", "--skills="}).exit).To(Equal(2))
	})
	It("exits 2 for a scope given without a value", func() {
		Expect(runArgv([]string{"skills", "list", "--skills"}).exit).To(Equal(2))
	})
})

var _ = Describe("--skills-path on skills list", func() {
	var dir string

	BeforeEach(func() {
		agentsFixture()
		dir = GinkgoT().TempDir()
		writeSkill(dir, "tdd", skillDoc("tdd", ""))
	})

	It("marks a skill found through it as an explicit path", func() {
		_, out := skillsOut("list", "--skills-path", dir)
		Expect(out["skills"].([]any)[0].(map[string]any)["source"]).To(Equal("path"))
	})
	It("reads the user directory as well as the path under the default scope", func() {
		_, out := skillsOut("list", "--skills-path", dir)
		Expect(out["counts"].(map[string]any)["roots"]).To(BeEquivalentTo(2))
	})
	It("reads the path alone under --skills none", func() {
		_, out := skillsOut("list", "--skills", "none", "--skills-path", dir)
		Expect(skillNames(out)).To(Equal([]any{"tdd"}))
	})
	It("is repeatable, in the = form too", func() {
		other := GinkgoT().TempDir()
		writeSkill(other, "code-review", skillDoc("code-review", ""))
		_, out := skillsOut("list", "--skills", "none", "--skills-path", dir, "--skills-path="+other)
		Expect(skillNames(out)).To(Equal([]any{"code-review", "tdd"}))
	})
	It("exits 2 for a relative directory", func() {
		Expect(runArgv([]string{"skills", "list", "--skills-path", "skills"}).exit).To(Equal(2))
	})
	It("names the rule for a relative directory", func() {
		r := runArgv([]string{"skills", "list", "--skills-path", "skills"})
		Expect(r.stderr).To(ContainSubstring("--skills-path entries must be absolute directories, got"))
	})
	It("exits 2 for an empty value", func() {
		Expect(runArgv([]string{"skills", "list", "--skills-path="}).exit).To(Equal(2))
	})
})

var _ = Describe("Positional directories beside the skills settings", func() {
	It("are refused beside --skills, naming the rule", func() {
		r := runArgv([]string{"skills", "list", "--skills", "project", absDir()})
		Expect(r.stderr).To(ContainSubstring("a directory argument replaces the --skills settings: give one or the other"))
	})
	It("are refused beside --skills-path", func() {
		Expect(runArgv([]string{"skills", "validate", "--skills-path", absDir(), absDir()}).exit).To(Equal(2))
	})
	It("are refused beside --skills-default", func() {
		Expect(runArgv([]string{"skills", "validate", "--skills-default", "tdd", absDir()}).exit).To(Equal(2))
	})
	It("remain the complete root list when given alone", func() {
		dir := GinkgoT().TempDir()
		writeSkill(dir, "tdd", skillDoc("tdd", ""))
		_, out := skillsOut("validate", "--json", dir)
		Expect(out["counts"].(map[string]any)["roots"]).To(BeEquivalentTo(1))
	})
})

var _ = Describe("--skills-default on skills validate", func() {
	It("is accepted, splitting comma-separated values, and leaves the listing unchanged", func() {
		agentsFixture()
		_, out := skillsOut("validate", "--skills", "none", "--skills-default", "tdd,code-*", "--skills-default=review")
		Expect(out["counts"]).To(Equal(map[string]any{"roots": 0.0, "loaded": 0.0, "errors": 0.0, "warnings": 0.0}))
	})

	DescribeTable("exits 2 for a malformed entry",
		func(value string) {
			Expect(runArgv([]string{"skills", "validate", "--skills-default", value}).exit).To(Equal(2))
		},
		Entry("empty", ""),
		Entry("blank", " "),
		Entry("a space inside", "a b"),
		Entry("a trailing comma", "tdd,"),
		Entry("a duplicate across values", "tdd,tdd"),
	)
})

var _ = Describe("Skills settings from --config on skills list and validate", func() {
	var dir, other, cfg string

	BeforeEach(func() {
		agentsFixture()
		dir, other = GinkgoT().TempDir(), GinkgoT().TempDir()
		writeSkill(dir, "tdd", skillDoc("tdd", ""))
		writeSkill(other, "code-review", skillDoc("code-review", ""))
		cfg = writeConfigFixture(fmt.Sprintf(`{"skills_paths":[%q]}`, dir))
	})

	It("lists the skills of the config's skills_paths", func() {
		_, out := skillsOut("list", "--skills", "none", "--config", cfg)
		Expect(skillNames(out)).To(Equal([]any{"tdd"}))
	})
	It("lets --skills-path replace the config's skills_paths", func() {
		_, out := skillsOut("list", "--skills", "none", "--config", cfg, "--skills-path", other)
		Expect(skillNames(out)).To(Equal([]any{"code-review"}))
	})
	It("lets a directory argument replace the config's skills_paths", func() {
		_, out := skillsOut("list", "--config", cfg, other)
		Expect(skillNames(out)).To(Equal([]any{"code-review"}))
	})
	It("exits 4 on validate for a relative skills_paths entry", func() {
		bad := writeConfigFixture(`{"skills_paths":["skills"]}`)
		Expect(runArgv([]string{"skills", "validate", "--config", bad}).exit).To(Equal(4))
	})
	It("exits 4 on list when the ambient flowstate.json names skills_paths", func() {
		path := filepath.Join(inTempDir(), "flowstate.json")
		if err := os.WriteFile(path, []byte(`{"skills_paths":["/skills"]}`), 0o600); err != nil {
			panic(err)
		}
		Expect(runArgv([]string{"skills", "list"}).exit).To(Equal(4))
	})
	It("exits 2 for --skills-path X and X/", func() {
		Expect(runArgv([]string{"skills", "list", "--skills-path", dir, "--skills-path", dir + "/"}).exit).To(Equal(2))
	})
	It("lists one root for the directory arguments X and X/", func() {
		_, out := skillsOut("list", dir, dir+"/")
		Expect(out["counts"].(map[string]any)["roots"]).To(BeEquivalentTo(1))
	})
	It("prints a project skill's path as absolute", func() {
		project := inTempDir()
		writeSkill(filepath.Join(project, ".flowstate", "skills"), "p", skillDoc("p", ""))
		_, out := skillsOut("list", "--skills", "project")
		Expect(filepath.IsAbs(out["skills"].([]any)[0].(map[string]any)["path"].(string))).To(BeTrue())
	})
})
