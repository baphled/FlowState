package cli_test

import (
	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// BootstrapAnnotation_test pins the annotation surface used by the cli
// root's PersistentPreRunE to gate app.Bootstrap. Pre-this spec, the
// MarkNeedsBootstrap helper was wired in root.go but no test asserted
// which leaf commands carried the annotation, so a regression that drops
// MarkNeedsBootstrap from the serve/chat/run wiring would silently
// re-introduce the bootstrap-fires-on-every-subcommand hazard (this time
// in the reverse direction: bootstrap silently NEVER fires for the
// commands that need it, so first-run users get a permissions.yaml-less
// install).
var _ = Describe("cli root command bootstrap annotations", func() {
	var (
		testApp *app.App
	)

	BeforeEach(func() {
		var err error
		testApp, err = app.NewForTest(app.TestConfig{})
		Expect(err).NotTo(HaveOccurred())
	})

	It("marks the bootstrap-needing commands with AnnotationBootstrap=true", func() {
		root := cli.NewRootCmd(testApp)

		// These leaves drive engine, agent registry, swarm registry,
		// tool catalogue, skills, vault tools, memory tools,
		// autoresearch, plan, or auth storage — every one expects the
		// seeded XDG_CONFIG layout. Adding a new bootstrap-needing
		// subcommand without extending this spec is intentional: the
		// spec exists to catch silent drops, not to enumerate.
		mustNeedBootstrap := []string{
			"chat",
			"auth",
			"run",
			"serve",
			"agent",
			"agents",
			"vault-tools",
			"vault",
			"memory-tools",
			"coordination",
			"autoresearch",
			"swarm",
			"skill",
			"discover",
			"plan",
			"tools",
		}
		for _, name := range mustNeedBootstrap {
			c, _, err := root.Find([]string{name})
			Expect(err).NotTo(HaveOccurred(), "command %q should be registered", name)
			Expect(c.Annotations[cli.AnnotationBootstrap]).To(Equal("true"),
				"command %q must carry AnnotationBootstrap=true", name)
		}
	})

	It("leaves read-only commands without AnnotationBootstrap", func() {
		root := cli.NewRootCmd(testApp)

		// `models`, `session`, and `config` operate on already-loaded
		// state (or pure config inspection); seeding the XDG_CONFIG
		// layout for those is wasted I/O and risks pollution when the
		// user is exploring with `flowstate --config=... session list`
		// against an isolated tempdir. Cobra built-ins (`--help`,
		// `--version`, unknown subcommand) bypass PersistentPreRunE
		// entirely so they need no annotation.
		mustNotNeedBootstrap := []string{
			"models",
			"session",
			"config",
		}
		for _, name := range mustNotNeedBootstrap {
			c, _, err := root.Find([]string{name})
			Expect(err).NotTo(HaveOccurred(), "command %q should be registered", name)
			Expect(c.Annotations[cli.AnnotationBootstrap]).To(BeEmpty(),
				"command %q must NOT carry AnnotationBootstrap", name)
		}
	})
})
