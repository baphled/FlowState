package cli_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("memory-tools install command", func() {
	var (
		out      *bytes.Buffer
		testApp  *app.App
		toolsDir string
		runCmd   func(args ...string) error
	)

	BeforeEach(func() {
		out = &bytes.Buffer{}
		toolsDir = filepath.Join(GinkgoT().TempDir(), "memory-tools")

		// NewForTest gives the command a real App without authenticating
		// against host providers, mirroring the vault-tools install
		// setup. The memory-tools install path does not consult
		// AgentsDir/SkillsDir but passing an empty AgentsDir is fine.
		var err error
		testApp, err = app.NewForTest(app.TestConfig{})
		Expect(err).NotTo(HaveOccurred())

		runCmd = func(args ...string) error {
			root := cli.NewRootCmd(testApp)
			root.SetOut(out)
			root.SetErr(out)
			root.SetArgs(args)
			return root.Execute()
		}
	})

	Context("when the target directory is empty", func() {
		It("creates the bundled JS server and its launcher with the executable bit set", func() {
			out.Reset()
			err := runCmd("memory-tools", "install", "--target", toolsDir)
			Expect(err).NotTo(HaveOccurred())

			// Both canonical payload entries must land on disk: the
			// bundled JS server and the bash wrapper that execs `node`
			// against it. Without the wrapper, the operator's PATH-based
			// `mcp-mem0-server` discovery in mcp_discover.go silently
			// breaks; without the JS, the wrapper has nothing to run.
			for _, name := range []string{"mcp-mem0-server.js", "mcp-mem0-server"} {
				path := filepath.Join(toolsDir, name)
				info, statErr := os.Stat(path)
				Expect(statErr).NotTo(HaveOccurred(), "expected %s to exist", name)

				// 0o755 — executable for owner, group, and other. The
				// wrapper is the binary on PATH; the .js does not need
				// the bit but inherits it harmlessly because `node` is
				// invoked with the file as an argument.
				mode := info.Mode().Perm()
				Expect(mode&0o100).NotTo(BeZero(), "owner exec bit missing on %s (mode=%o)", name, mode)

				// Content must match the embedded payload byte-for-byte
				// — verbatim copy is the v1 contract; templating is a
				// follow-up.
				gotBytes, readErr := os.ReadFile(path) //nolint:gosec // path is a tempdir join
				Expect(readErr).NotTo(HaveOccurred())
				wantBytes, embedErr := fs.ReadFile(app.EmbeddedMemoryToolsFS(), "memory_tools/"+name)
				Expect(embedErr).NotTo(HaveOccurred())
				Expect(gotBytes).To(Equal(wantBytes), "%s differs from embedded source", name)
			}

			Expect(out.String()).To(ContainSubstring("created"))
		})
	})

	Context("when --dry-run is set against an empty target", func() {
		It("reports what would change without writing any files", func() {
			out.Reset()
			err := runCmd("memory-tools", "install", "--target", toolsDir, "--dry-run")
			Expect(err).NotTo(HaveOccurred())

			Expect(out.String()).To(ContainSubstring("dry-run"))
			Expect(out.String()).To(ContainSubstring("created"))

			// The directory might not even exist — and definitely the
			// payload must not be written.
			_, statErr := os.Stat(filepath.Join(toolsDir, "mcp-mem0-server.js"))
			Expect(os.IsNotExist(statErr)).To(BeTrue(), "dry-run must not write")
		})
	})

	Context("when a payload entry already exists with operator edits", func() {
		BeforeEach(func() {
			Expect(os.MkdirAll(toolsDir, 0o755)).To(Succeed())
			operatorEdited := []byte("// operator edited\nconsole.log('local fork');\n")
			Expect(os.WriteFile(filepath.Join(toolsDir, "mcp-mem0-server.js"), operatorEdited, 0o755)).To(Succeed())
		})

		It("skips the file by default to preserve operator edits", func() {
			out.Reset()
			err := runCmd("memory-tools", "install", "--target", toolsDir)
			Expect(err).NotTo(HaveOccurred())

			Expect(out.String()).To(ContainSubstring("skipped"))
			Expect(out.String()).To(ContainSubstring("mcp-mem0-server.js"))

			content, readErr := os.ReadFile(filepath.Join(toolsDir, "mcp-mem0-server.js")) //nolint:gosec // tempdir join
			Expect(readErr).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("operator edited"),
				"default install must not clobber operator edits")
		})

		It("overwrites with the embedded version when --force is set", func() {
			out.Reset()
			err := runCmd("memory-tools", "install", "--target", toolsDir, "--force")
			Expect(err).NotTo(HaveOccurred())

			Expect(out.String()).To(ContainSubstring("updated"))

			content, readErr := os.ReadFile(filepath.Join(toolsDir, "mcp-mem0-server.js")) //nolint:gosec // tempdir join
			Expect(readErr).NotTo(HaveOccurred())
			Expect(string(content)).NotTo(ContainSubstring("operator edited"))

			embedded, embedErr := fs.ReadFile(app.EmbeddedMemoryToolsFS(), "memory_tools/mcp-mem0-server.js")
			Expect(embedErr).NotTo(HaveOccurred())
			Expect(content).To(Equal(embedded), "force install must match embedded byte-for-byte")
		})
	})

	Context("when a payload entry already matches the embedded version", func() {
		It("reports it as unchanged", func() {
			Expect(runCmd("memory-tools", "install", "--target", toolsDir)).To(Succeed())

			out.Reset()
			err := runCmd("memory-tools", "install", "--target", toolsDir)
			Expect(err).NotTo(HaveOccurred())

			Expect(out.String()).To(ContainSubstring("unchanged"))
		})
	})

	Context("when --verbose is set", func() {
		It("includes size deltas in the report", func() {
			out.Reset()
			err := runCmd("memory-tools", "install", "--target", toolsDir, "--verbose")
			Expect(err).NotTo(HaveOccurred())

			Expect(out.String()).To(MatchRegexp(`\d+\s*->\s*\d+|bytes`))
		})
	})
})
