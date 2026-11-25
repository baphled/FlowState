package main

import (
	"errors"
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
