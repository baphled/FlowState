package app_test

import (
	"bytes"
	"log"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/config"
)

// Bootstrap_hazard_test pins the bootstrap-fires-on-every-subcommand fix.
// Pre-fix, `app.New` ran eight XDG-mutating side effects unconditionally
// (agent/skill/swarm/gate migration + seeding, mem0 wrapper materialisation,
// permissions.yaml write). That meant `flowstate --version`, `flowstate
// help`, `flowstate <typo>`, and any flag-parsing failure all materialised
// the seven seed directories AND permissions.yaml in whichever
// XDG_CONFIG_HOME resolved at the time — the precise hazard the engineer
// hit during Slice C verification when a stray smoke leaked a real
// permissions.yaml into their personal config dir.
//
// The fix splits construction from bootstrap: `app.NewWithOptions(cfg,
// app.NewOptions{SkipBootstrap: true})` returns an App without firing the
// eight side effects; callers (or the cli root's PersistentPreRunE) invoke
// `app.Bootstrap(cfg)` explicitly for the commands that need it. These
// specs assert both halves of that contract.
var _ = Describe("App bootstrap isolation", func() {
	var (
		tmpDir          string
		fakeHome        string
		originalXDG     string
		originalHome    string
		originalXDGData string
		cfg             *config.AppConfig
	)

	BeforeEach(func() {
		tmpDir = GinkgoT().TempDir()
		fakeHome = GinkgoT().TempDir()

		originalXDG = os.Getenv("XDG_CONFIG_HOME")
		originalHome = os.Getenv("HOME")
		originalXDGData = os.Getenv("XDG_DATA_HOME")
		Expect(os.Setenv("XDG_CONFIG_HOME", tmpDir)).To(Succeed())
		// Isolate $HOME so DefaultMemoryToolsDir (which reads HOME via
		// os.UserHomeDir, not XDG_DATA_HOME) lands inside fakeHome too.
		// XDG_DATA_HOME isolation covers cfg.DataDir downstream uses;
		// without it cfg.DataDir would still point at the real
		// ~/.local/share/flowstate so a defensive override here keeps
		// the spec deterministic across hosts.
		Expect(os.Setenv("HOME", fakeHome)).To(Succeed())
		Expect(os.Setenv("XDG_DATA_HOME", filepath.Join(fakeHome, "data"))).To(Succeed())

		// DefaultConfig resolves AgentDir/SkillDir/GatesDir against Dir()
		// at call time, so it MUST be constructed after the env vars
		// above so the resolved paths sit inside tmpDir.
		cfg = config.DefaultConfig()
		// DefaultConfig pins Providers.Default to "anthropic"; switch to
		// openai with a throwaway key so NewWithOptions can resolve a
		// default provider without contacting any live API. Mirrors the
		// pattern already used in internal/cli/auth_test.go.
		cfg.Providers.Default = "openai"
		Expect(os.Setenv("OPENAI_API_KEY", "test-key-bootstrap-isolation")).To(Succeed())
	})

	AfterEach(func() {
		Expect(os.Unsetenv("OPENAI_API_KEY")).To(Succeed())
		if originalXDG != "" {
			Expect(os.Setenv("XDG_CONFIG_HOME", originalXDG)).To(Succeed())
		} else {
			Expect(os.Unsetenv("XDG_CONFIG_HOME")).To(Succeed())
		}
		if originalHome != "" {
			Expect(os.Setenv("HOME", originalHome)).To(Succeed())
		} else {
			Expect(os.Unsetenv("HOME")).To(Succeed())
		}
		if originalXDGData != "" {
			Expect(os.Setenv("XDG_DATA_HOME", originalXDGData)).To(Succeed())
		} else {
			Expect(os.Unsetenv("XDG_DATA_HOME")).To(Succeed())
		}
	})

	Describe("NewWithOptions with SkipBootstrap=true", func() {
		// This is the read-only `flowstate --version` / `flowstate help`
		// / `flowstate <typo>` shape: cmd/flowstate/main.go constructs
		// the App via NewWithOptions(SkipBootstrap:true) before Cobra
		// resolves a subcommand; PersistentPreRunE never runs for built-
		// in cobra paths (--help, --version, unknown subcommand), so
		// app.Bootstrap is never invoked. Nothing should appear in
		// XDG_CONFIG_HOME.

		It("does not create permissions.yaml in the config dir", func() {
			// Silence the log chatter (provider warnings etc.) so the
			// spec output stays readable when run with -v.
			log.SetOutput(&bytes.Buffer{})
			DeferCleanup(func() { log.SetOutput(os.Stderr) })

			_, err := app.NewWithOptions(cfg, app.NewOptions{SkipBootstrap: true})
			Expect(err).NotTo(HaveOccurred())

			permsPath := filepath.Join(config.Dir(), "permissions.yaml")
			_, statErr := os.Stat(permsPath)
			Expect(os.IsNotExist(statErr)).To(BeTrue(),
				"permissions.yaml MUST NOT be written when SkipBootstrap=true; "+
					"found file at %s", permsPath)
		})

		It("does not create the agents/skills/swarms/gates seed directories", func() {
			log.SetOutput(&bytes.Buffer{})
			DeferCleanup(func() { log.SetOutput(os.Stderr) })

			_, err := app.NewWithOptions(cfg, app.NewOptions{SkipBootstrap: true})
			Expect(err).NotTo(HaveOccurred())

			// Each of the four seed dirs is owned by Bootstrap. Without
			// it none of them should appear on disk. setupAgentRegistry /
			// setupSwarmRegistry / loadSkills walk the (empty) dirs but
			// must not create them — that distinguishes "load registries
			// from disk" (always safe) from "seed defaults to disk"
			// (Bootstrap-only).
			for _, dir := range []string{
				cfg.AgentDir,
				cfg.SkillDir,
				filepath.Join(config.Dir(), "swarms"),
				cfg.GatesDir,
			} {
				_, statErr := os.Stat(dir)
				Expect(os.IsNotExist(statErr)).To(BeTrue(),
					"seed directory MUST NOT be created when "+
						"SkipBootstrap=true; found %s", dir)
			}
		})
	})

	Describe("Bootstrap invoked explicitly", func() {
		// This is the `flowstate serve` / `flowstate chat` / `flowstate
		// run` shape: PersistentPreRunE invokes app.Bootstrap once the
		// resolved command carries cli.AnnotationBootstrap. After
		// Bootstrap fires, permissions.yaml + the seed dirs are present.
		// Captures the positive contract so a future refactor that
		// silently drops a side effect from Bootstrap is caught.

		It("creates permissions.yaml and the seed directories", func() {
			log.SetOutput(&bytes.Buffer{})
			DeferCleanup(func() { log.SetOutput(os.Stderr) })

			app.Bootstrap(cfg)

			Expect(filepath.Join(config.Dir(), "permissions.yaml")).
				To(BeAnExistingFile())
			for _, dir := range []string{
				cfg.AgentDir,
				cfg.SkillDir,
				filepath.Join(config.Dir(), "swarms"),
				cfg.GatesDir,
			} {
				info, statErr := os.Stat(dir)
				Expect(statErr).NotTo(HaveOccurred(),
					"Bootstrap MUST create seed dir %s", dir)
				Expect(info.IsDir()).To(BeTrue(),
					"%s should be a directory, got mode %v", dir, info.Mode())
			}
		})
	})
})
