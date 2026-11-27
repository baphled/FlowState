package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
)

func writeConfigFixture(body string) string {
	path := filepath.Join(GinkgoT().TempDir(), "flowstate.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		panic(err)
	}
	return path
}

func loadConfigFixture(path string) (cliConfig, string) {
	cfg, source, err := loadConfig(path)
	if err != nil {
		panic(err)
	}
	return cfg, source
}

const validConfigFixture = `{
	"default_provider": "z.ai",
	"default_model": "glm-5.3",
	"default_budget": 5000,
	"storage_path": "/tmp/flowstate.db",
	"tool_allowlist": ["echo", "fs.read"]
}`

var _ = Describe("Config parsing and canonical composition", func() {
	It("parses the valid schema", func() {
		cfg, _ := loadConfigFixture(writeConfigFixture(validConfigFixture))
		Expect(cfg).To(Equal(cliConfig{
			DefaultProvider: "z.ai",
			DefaultModel:    "glm-5.3",
			DefaultBudget:   5000,
			StoragePath:     "/tmp/flowstate.db",
			ToolAllowlist:   []string{"echo", "fs.read"},
		}))
	})

	It("rejects an unknown field", func() {
		_, _, err := loadConfig(writeConfigFixture(`{"default_provider":"z.ai","unexpected":1}`))
		Expect(errors.Is(err, app.ErrConfigInvalid)).To(BeTrue())
	})

	It("rejects a top-level api_key", func() {
		_, _, err := loadConfig(writeConfigFixture(`{"default_provider":"z.ai","api_key":"x"}`))
		Expect(errors.Is(err, app.ErrConfigInvalid)).To(BeTrue())
	})

	It("rejects a nested secret key", func() {
		_, _, err := loadConfig(writeConfigFixture(`{"default_provider":"z.ai","inner":{"password":"x"}}`))
		Expect(errors.Is(err, app.ErrConfigInvalid)).To(BeTrue())
	})

	It("rejects a negative default budget", func() {
		_, _, err := loadConfig(writeConfigFixture(`{"default_provider":"z.ai","default_budget":-1}`))
		Expect(errors.Is(err, app.ErrConfigInvalid)).To(BeTrue())
	})

	It("rejects duplicate tool_allowlist entries", func() {
		_, _, err := loadConfig(writeConfigFixture(`{"default_provider":"z.ai","tool_allowlist":["echo","echo"]}`))
		Expect(errors.Is(err, app.ErrConfigInvalid)).To(BeTrue())
	})

	It("keeps the canonical hash stable across repeated loads", func() {
		path := writeConfigFixture(validConfigFixture)
		first, _ := loadConfigFixture(path)
		second, _ := loadConfigFixture(path)
		firstSHA, ferr := compositionSHA256(first.DefaultProvider, first.DefaultModel, first.StoragePath, path, first.ToolAllowlist)
		if ferr != nil {
			panic(ferr)
		}
		secondSHA, serr := compositionSHA256(second.DefaultProvider, second.DefaultModel, second.StoragePath, path, second.ToolAllowlist)
		if serr != nil {
			panic(serr)
		}
		Expect(firstSHA).To(Equal(secondSHA))
	})

	It("changes the canonical hash when a field changes", func() {
		left, lerr := compositionSHA256("z.ai", "glm-5.3", "/x/flowstate.db", configSourceNone, []string{"echo"})
		if lerr != nil {
			panic(lerr)
		}
		right, rerr := compositionSHA256("z.ai", "glm-4", "/x/flowstate.db", configSourceNone, []string{"echo"})
		if rerr != nil {
			panic(rerr)
		}
		Expect(left).NotTo(Equal(right))
	})
})

var _ = DescribeTable("effectiveAllowlist (explicit allowlist flags replace config; --workdir does not)",
	func(fl flags, want []string) {
		Expect(effectiveAllowlist(&fl)).To(Equal(want))
	},
	Entry("no tool flag, config allowlist present: the config list",
		flags{config: cliConfig{ToolAllowlist: []string{"echo"}}}, []string{"echo"}),
	Entry("no tool flag, config allowlist absent: empty",
		flags{}, []string(nil)),
	Entry("--allow, config allowlist present: the flags replace the config",
		flags{allow: []string{"fs.read"}, config: cliConfig{ToolAllowlist: []string{"echo"}}}, []string{"fs.read"}),
	Entry("--allow-file, config allowlist present: the config is replaced, not merged",
		flags{allowFile: "m.json", config: cliConfig{ToolAllowlist: []string{"echo"}}}, []string(nil)),
	Entry("--workdir alone, config allowlist present: the config list",
		flags{workdir: "/w", config: cliConfig{ToolAllowlist: []string{"echo"}}}, []string{"echo"}),
	Entry("--workdir alone, config allowlist absent: empty",
		flags{workdir: "/w"}, []string(nil)),
	Entry("--workdir with --allow, config allowlist present: the flags",
		flags{workdir: "/w", allow: []string{"fs.read"}, config: cliConfig{ToolAllowlist: []string{"echo"}}},
		[]string{"fs.read"}),
)

func compactionFixture(body string) (cliConfig, error) {
	return parseConfig([]byte(`{"default_provider":"z.ai","compaction":` + body + `}`))
}

var _ = Describe("Config compaction object", func() {
	It("applies the defaults when compaction is absent", func() {
		cfg, err := parseConfig([]byte(`{"default_provider":"z.ai"}`))
		if err != nil {
			panic(err)
		}
		Expect(cfg.Compaction.Resolved()).To(Equal(app.CompactionSettings{Enabled: true, Reserve: 16384, KeepRecent: 20000}))
	})

	It("accepts the four ADR names", func() {
		cfg, err := compactionFixture(`{"enabled":false,"reserve":1000,"keep_recent":2000,"context_window":32768}`)
		if err != nil {
			panic(err)
		}
		Expect(cfg.Compaction.Resolved()).To(Equal(app.CompactionSettings{Reserve: 1000, KeepRecent: 2000, ContextWindow: 32768}))
	})

	It("keeps the defaults for the keys it omits", func() {
		cfg, err := compactionFixture(`{"reserve":1000}`)
		if err != nil {
			panic(err)
		}
		Expect(cfg.Compaction.Resolved().KeepRecent).To(Equal(20000))
	})

	It("rejects a token-named key with the secret-key message", func() {
		_, err := compactionFixture(`{"reserve_tokens":1000}`)
		Expect(err).To(MatchError(ContainSubstring(`config key "reserve_tokens" looks like a secret`)))
	})

	It("rejects an unknown key", func() {
		_, err := compactionFixture(`{"window":1000}`)
		Expect(err).To(MatchError(ContainSubstring("unknown field")))
	})

	DescribeTable("rejects as config_invalid",
		func(body string) {
			_, err := compactionFixture(body)
			Expect(errors.Is(err, app.ErrConfigInvalid)).To(BeTrue(), "got %v", err)
		},
		Entry("a zero reserve", `{"reserve":0}`),
		Entry("a negative reserve", `{"reserve":-5}`),
		Entry("a string reserve", `{"reserve":"big"}`),
		Entry("a bool reserve", `{"reserve":true}`),
		Entry("a fractional reserve", `{"reserve":1.5}`),
		Entry("a zero keep_recent", `{"keep_recent":0}`),
		Entry("a string keep_recent", `{"keep_recent":"1"}`),
		Entry("a zero window", `{"context_window":0}`),
		Entry("an absurd window", `{"context_window":1000000000}`),
		Entry("a string window", `{"context_window":"abc"}`),
		Entry("a bool window", `{"context_window":false}`),
		Entry("a string enabled", `{"enabled":"yes"}`),
		Entry("a numeric enabled", `{"enabled":1}`),
		Entry("a non-object compaction", `[]`),
	)
})

// baseComposition is the canonical composition of the pinned inputs of the skills hash specs as it was recorded
// before the skills keys existed (ADR-013 D16): empty skills settings, and none at all, must reproduce it byte for
// byte, so every config_sha256 recorded before this change still matches.
const baseComposition = `{"config_source":"none","db_path":"flowstate.db","harness":"flowstate-cli",` +
	`"model":"glm-5.3","provider":"z.ai","tool_allowlist":["echo"]}`

// baseCompositionSHA is the sha256 of baseComposition.
const baseCompositionSHA = "b3b7c7760cda0b96a7a97ecabe6310db9b0a76292689d51ee212984e4a42f87b"

// inTempDir moves the process working directory to a fresh temp directory for the spec, so an ambient
// ./flowstate.json or a project .flowstate directory can be staged, and restores it afterwards.
func inTempDir() string {
	previous, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	dir := GinkgoT().TempDir()
	if err := os.Chdir(dir); err != nil {
		panic(err)
	}
	DeferCleanup(func() {
		if err := os.Chdir(previous); err != nil {
			panic(err)
		}
	})
	return dir
}

// absDir is an absolute directory below a fresh temp directory, so a spec's path is absolute on every OS.
func absDir(names ...string) string {
	return filepath.Join(append([]string{GinkgoT().TempDir()}, names...)...)
}

// skillsRoot is an absolute directory name on every OS ("/skills" is not absolute on Windows).
var skillsRoot = filepath.Join(filepath.VolumeName(os.TempDir())+string(filepath.Separator), "skills")

var _ = Describe("Skills settings in an explicit --config file", func() {
	DescribeTable("parse as flat keys",
		func(body string, want cliConfig) {
			cfg, err := parseConfig([]byte(body))
			if err != nil {
				panic(err)
			}
			Expect(cfg).To(Equal(want))
		},
		Entry("skills_paths", fmt.Sprintf(`{"skills_paths":[%q,%q]}`, skillsRoot+"a", skillsRoot+"b"),
			cliConfig{SkillsPaths: []string{skillsRoot + "a", skillsRoot + "b"}}),
		Entry("skills_default", `{"skills_default":["tdd","code-*"]}`,
			cliConfig{SkillsDefault: []string{"tdd", "code-*"}}),
		Entry("mixed-case keys, which the decoder matches", fmt.Sprintf(`{"Skills_Paths":[%q],"SKILLS_DEFAULT":["tdd"]}`, skillsRoot),
			cliConfig{SkillsPaths: []string{skillsRoot}, SkillsDefault: []string{"tdd"}}),
	)

	DescribeTable("refuse as config_invalid",
		func(body string) {
			_, err := parseConfig([]byte(body))
			Expect(errors.Is(err, app.ErrConfigInvalid)).To(BeTrue(), "got %v", err)
		},
		Entry("a relative skills_paths entry", `{"skills_paths":["skills"]}`),
		Entry("an empty skills_paths entry", `{"skills_paths":[""]}`),
		Entry("a duplicate skills_paths entry", fmt.Sprintf(`{"skills_paths":[%q,%q]}`, skillsRoot, skillsRoot)),
		Entry("a skills_paths entry equal after Clean", fmt.Sprintf(`{"skills_paths":[%q,%q]}`, skillsRoot, skillsRoot+"/")),
		Entry("an empty skills_default entry", `{"skills_default":[""]}`),
		Entry("a blank skills_default entry", `{"skills_default":[" "]}`),
		Entry("a skills_default entry with a space inside", `{"skills_default":["a b"]}`),
		Entry("a duplicate skills_default entry", `{"skills_default":["tdd","tdd"]}`),
	)

	It("names a relative entry and the rule", func() {
		_, err := parseConfig([]byte(`{"skills_paths":["skills"]}`))
		Expect(err).To(MatchError(ContainSubstring(`skills_paths entries must be absolute directories, got "skills"`)))
	})

	It("names a duplicate skills_default entry", func() {
		_, err := parseConfig([]byte(`{"skills_default":["tdd","tdd"]}`))
		Expect(err).To(MatchError(ContainSubstring(`duplicate skills_default entry "tdd"`)))
	})

	// explicitSHA loads an explicit --config file and hashes its composition under one fixed source
	// label, so only the key spelling differs between the two files.
	explicitSHA := func(body string) string {
		cfg, _, err := loadConfig(writeConfigFixture(body))
		if err != nil {
			panic(err)
		}
		sha, err := compositionSHA256(cfg.DefaultProvider, cfg.DefaultModel, cfg.StoragePath, "explicit", nil,
			skillsSettings{Paths: cfg.SkillsPaths, Default: cfg.SkillsDefault})
		if err != nil {
			panic(err)
		}
		return sha
	}

	It("hashes mixed-case skills keys identically to the exact-case spelling", func() {
		Expect(explicitSHA(fmt.Sprintf(`{"Skills_Paths":[%q],"SKILLS_DEFAULT":["tdd"]}`, skillsRoot))).To(
			Equal(explicitSHA(fmt.Sprintf(`{"skills_paths":[%q],"skills_default":["tdd"]}`, skillsRoot))))
	})
})

var _ = Describe("The ambient flowstate.json and the skills settings", func() {
	var dir string

	BeforeEach(func() { dir = inTempDir() })

	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "flowstate.json"), []byte(body), 0o600); err != nil {
			panic(err)
		}
	}

	// The decoder matches keys case-insensitively and folds the long s (U+017F) and the Kelvin sign
	// (U+212A), so Skills_Paths, SKILLS_DEFAULT, skillſ_paths and sKills_paths set the skills as surely
	// as the exact spelling; a null value decodes to nil, so only the key scan refuses it.
	DescribeTable("is config_invalid when it names a skills key, whatever the value and case",
		func(body string) {
			write(body)
			_, _, err := loadConfig("")
			Expect(errors.Is(err, app.ErrConfigInvalid)).To(BeTrue(), "got %v", err)
		},
		Entry("skills_paths with a directory", `{"skills_paths":["/skills"]}`),
		Entry("skills_paths empty", `{"skills_paths":[]}`),
		Entry("skills_paths null", `{"skills_paths":null}`),
		Entry("skills_default with a name", `{"skills_default":["tdd"]}`),
		Entry("skills_default null", `{"skills_default":null}`),
		Entry("Skills_Paths with a directory", `{"Skills_Paths":["/skills"]}`),
		Entry("SKILLS_DEFAULT with a name", `{"SKILLS_DEFAULT":["tdd"]}`),
		Entry("Skills_Default empty", `{"Skills_Default":[]}`),
		Entry("Skills_Default null", `{"Skills_Default":null}`),
		Entry("skillſ_paths (long s) with a directory", `{"skillſ_paths":["/skills"]}`),
		Entry("skillſ_paths (long s) null", `{"skillſ_paths":null}`),
		Entry("sKills_paths (Kelvin sign) with a directory", `{"sKills_paths":["/skills"]}`),
	)

	It("says to pass them by flag or in an explicit --config file", func() {
		write(`{"skills_default":["tdd"]}`)
		_, _, err := loadConfig("")
		Expect(err.Error()).To(ContainSubstring("the ambient flowstate.json cannot set skills_paths or skills_default: " +
			"pass them with --skills-path and --skills-default, or in an explicit --config file"))
	})

	It("still loads an ambient file that sets neither", func() {
		write(`{"default_provider":"z.ai"}`)
		cfg, _, err := loadConfig("")
		if err != nil {
			panic(err)
		}
		Expect(cfg.DefaultProvider).To(Equal("z.ai"))
	})

	It("accepts the same keys from an explicit --config file", func() {
		path := writeConfigFixture(`{"skills_default":["tdd"]}`)
		cfg, _, err := loadConfig(path)
		if err != nil {
			panic(err)
		}
		Expect(cfg.SkillsDefault).To(Equal([]string{"tdd"}))
	})

	It("exits 4 for session new from the ambient file", func() {
		write(`{"default_provider":"z.ai","default_model":"m","skills_paths":["/skills"]}`)
		Expect(runCLINew("--task", "t").exit).To(Equal(4))
	})

	It("exits 4 for session new from a mixed-case skills key with a null value", func() {
		write(`{"default_provider":"z.ai","default_model":"m","Skills_Default":null}`)
		Expect(runCLINew("--task", "t").exit).To(Equal(4))
	})
})

var _ = Describe("Skills settings in the composition hash", func() {
	hash := func(sk skillsSettings) string {
		sha, err := compositionSHA256("z.ai", "glm-5.3", "flowstate.db", configSourceNone, []string{"echo"}, sk)
		if err != nil {
			panic(err)
		}
		return sha
	}
	body := func(sk skillsSettings) string {
		body, err := canonicalComposition("z.ai", "glm-5.3", app.DefaultHarness, "flowstate.db", configSourceNone, []string{"echo"}, sk)
		if err != nil {
			panic(err)
		}
		return string(body)
	}
	keys := func(sk skillsSettings) map[string]any {
		var got map[string]any
		if err := json.Unmarshal([]byte(body(sk)), &got); err != nil {
			panic(err)
		}
		return got
	}

	It("reproduces the hash recorded before the skills keys existed when both are empty", func() {
		Expect(hash(skillsSettings{Paths: []string{}, Default: []string{}})).To(Equal(baseCompositionSHA))
	})

	It("emits neither skills key when both are empty", func() {
		Expect(body(skillsSettings{})).To(Equal(baseComposition))
	})

	It("reproduces it for a caller that passes no settings", func() {
		sha, err := compositionSHA256("z.ai", "glm-5.3", "flowstate.db", configSourceNone, []string{"echo"})
		if err != nil {
			panic(err)
		}
		Expect(sha).To(Equal(baseCompositionSHA))
	})

	It("changes when skills_paths is set", func() {
		Expect(hash(skillsSettings{Paths: []string{"/skills"}})).NotTo(Equal(baseCompositionSHA))
	})

	It("changes when skills_default is set", func() {
		Expect(hash(skillsSettings{Default: []string{"tdd"}})).NotTo(Equal(baseCompositionSHA))
	})

	It("cleans and sorts the paths", func() {
		abs := absDir()
		Expect(keys(skillsSettings{Paths: []string{abs + "/z", abs + "/a/../b"}})["skills_paths"]).To(
			Equal([]any{filepath.Join(abs, "b"), filepath.Join(abs, "z")}))
	})

	It("sorts the names", func() {
		Expect(keys(skillsSettings{Default: []string{"tdd", "code-*"}})["skills_default"]).To(Equal([]any{"code-*", "tdd"}))
	})
})

var _ = DescribeTable("effectiveSkills (each flag replaces the config key it names)",
	func(fl flags, want skillsSettings) {
		Expect(effectiveSkills(&fl)).To(Equal(want))
	},
	Entry("nothing given: empty", flags{}, skillsSettings{}),
	Entry("the config keys alone", flags{config: cliConfig{SkillsPaths: []string{"/a"}, SkillsDefault: []string{"tdd"}}},
		skillsSettings{Paths: []string{"/a"}, Default: []string{"tdd"}}),
	Entry("--skills-path replaces the config paths and keeps its default",
		flags{skillsPaths: []string{"/b"}, config: cliConfig{SkillsPaths: []string{"/a"}, SkillsDefault: []string{"tdd"}}},
		skillsSettings{Paths: []string{"/b"}, Default: []string{"tdd"}}),
	Entry("--skills-default replaces the config default and keeps its paths",
		flags{skillsDefault: []string{"x"}, config: cliConfig{SkillsPaths: []string{"/a"}, SkillsDefault: []string{"tdd"}}},
		skillsSettings{Paths: []string{"/a"}, Default: []string{"x"}}),
)

var _ = Describe("A session created with --config carrying the skills settings", func() {
	It("records a composition hash that includes them", func() {
		paths := absDir("a", "skills")
		path := writeConfigFixture(fmt.Sprintf(`{"default_provider":"z.ai","default_model":"m","skills_paths":[%q],"skills_default":["tdd"]}`, paths))
		created := runCLINew("--config", path, "--task", "t")
		if created.exit != 0 {
			panic(created.stderr)
		}
		want, err := compositionSHA256("z.ai", "m", dbFor(), path, nil, skillsSettings{Paths: []string{paths}, Default: []string{"tdd"}})
		if err != nil {
			panic(err)
		}
		Expect(sessionRowConfigSHA(decodeNew(created.stdout).ID)).To(Equal(want))
	})
})

// sessionRowConfigSHA reads the recorded config_sha256 of one session through `session list`.
func sessionRowConfigSHA(id string) string {
	for _, line := range lines(runCLIList().stdout) {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			panic(err)
		}
		if row["id"] == id {
			return row["config_sha256"].(string)
		}
	}
	panic("session row not found: " + id)
}
