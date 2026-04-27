package cli_test

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("swarm command", func() {
	var (
		out      *bytes.Buffer
		errBuf   *bytes.Buffer
		testApp  *app.App
		swarmDir string
		runCmd   func(args ...string) error
	)

	writeManifest := func(name, body string) {
		path := filepath.Join(swarmDir, name)
		Expect(os.WriteFile(path, []byte(body), 0o600)).To(Succeed())
	}

	validManifest := func(id, lead string) string {
		return "schema_version: \"1.0.0\"\n" +
			"id: " + id + "\n" +
			"lead: " + lead + "\n" +
			"members: []\n"
	}

	manifestWithGates := func(id, lead string) string {
		return "schema_version: \"1.0.0\"\n" +
			"id: " + id + "\n" +
			"lead: " + lead + "\n" +
			"members: [reviewer]\n" +
			"harness:\n" +
			"  gates:\n" +
			"    - name: g1\n" +
			"      kind: builtin:noop\n" +
			"    - name: g2\n" +
			"      kind: builtin:noop\n"
	}

	BeforeEach(func() {
		out = &bytes.Buffer{}
		errBuf = &bytes.Buffer{}
		dataDir := GinkgoT().TempDir()
		swarmDir = GinkgoT().TempDir()

		var err error
		testApp, err = app.NewForTest(app.TestConfig{DataDir: dataDir})
		Expect(err).NotTo(HaveOccurred())

		runCmd = func(args ...string) error {
			root := cli.NewRootCmd(testApp)
			root.SetOut(out)
			root.SetErr(errBuf)
			full := append([]string{"swarm"}, args...)
			full = append(full, "--swarm-dir", swarmDir)
			root.SetArgs(full)
			return root.Execute()
		}
	})

	Describe("list", func() {
		Context("when the swarm directory contains manifests", func() {
			BeforeEach(func() {
				writeManifest("alpha.yml", validManifest("alpha", "planner"))
				writeManifest("beta.yml", manifestWithGates("beta", "executor"))
			})

			It("prints every manifest with id, lead, member count, and gate count", func() {
				err := runCmd("list")
				Expect(err).NotTo(HaveOccurred())

				output := out.String()
				Expect(output).To(ContainSubstring("alpha"))
				Expect(output).To(ContainSubstring("planner"))
				Expect(output).To(ContainSubstring("beta"))
				Expect(output).To(ContainSubstring("executor"))
			})

			It("includes the gate count column for manifests with gates", func() {
				err := runCmd("list")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(MatchRegexp(`beta\s+executor\s+1\s+2`))
			})
		})

		Context("when the swarm directory is empty", func() {
			It("prints a friendly empty-state message and exits 0", func() {
				err := runCmd("list")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("no swarms registered"))
			})
		})

		Context("when the swarm directory does not exist", func() {
			BeforeEach(func() {
				swarmDir = filepath.Join(GinkgoT().TempDir(), "missing")
			})

			It("treats it as empty and exits 0", func() {
				err := runCmd("list")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("no swarms registered"))
			})
		})
	})

	Describe("validate", func() {
		Context("when every manifest is valid", func() {
			BeforeEach(func() {
				writeManifest("alpha.yml", validManifest("alpha", "planner"))
				writeManifest("beta.yml", validManifest("beta", "executor"))
			})

			It("reports pass for each and exits 0", func() {
				err := runCmd("validate")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("alpha"))
				Expect(out.String()).To(ContainSubstring("PASS"))
				Expect(out.String()).To(ContainSubstring("beta"))
			})

			It("validates a single manifest by id with no output on success", func() {
				err := runCmd("validate", "alpha")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("alpha"))
				Expect(out.String()).To(ContainSubstring("PASS"))
			})
		})

		Context("when a manifest is malformed", func() {
			BeforeEach(func() {
				writeManifest("alpha.yml", validManifest("alpha", "planner"))
				writeManifest("broken.yml",
					"schema_version: \"1.0.0\"\nid: \nlead: \nmembers: []\n")
			})

			It("exits 1 with the offending path in the error", func() {
				err := runCmd("validate")
				Expect(err).To(HaveOccurred())
				Expect(err.Error() + errBuf.String() + out.String()).To(
					ContainSubstring("broken.yml"))
			})
		})

		Context("when validating a single id that does not exist", func() {
			BeforeEach(func() {
				writeManifest("alpha.yml", validManifest("alpha", "planner"))
			})

			It("returns an error naming the missing id", func() {
				err := runCmd("validate", "missing-id")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("missing-id"))
			})
		})
	})

	Describe("run", func() {
		It("returns a not-yet-implemented error pointing at engine integration", func() {
			err := runCmd("run", "@solo")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not yet implemented"))
			Expect(err.Error()).To(ContainSubstring("engine integration"))
		})
	})
})
