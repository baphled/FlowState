package cli_test

import (
	"bytes"
	"path/filepath"
	"runtime"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func testdataPath(subdir string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", subdir)
}

func createTestApp(agentsDir, skillsDir string) *app.App {
	tc := app.TestConfig{
		AgentsDir: agentsDir,
		SkillsDir: skillsDir,
	}
	testApp, err := app.NewForTest(tc)
	Expect(err).NotTo(HaveOccurred())
	return testApp
}

var _ = Describe("CLI Commands", func() {
	var (
		out *bytes.Buffer
		cmd = func(testApp *app.App, args ...string) error {
			root := cli.NewRootCmd(testApp)
			root.SetOut(out)
			root.SetErr(out)
			root.SetArgs(args)
			return root.Execute()
		}
	)

	BeforeEach(func() {
		out = new(bytes.Buffer)
	})

	Describe("root --help", func() {
		It("shows usage information", func() {
			testApp := createTestApp("", "")
			err := cmd(testApp, "--help")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("FlowState provides an AI assistant TUI"))
			Expect(out.String()).To(ContainSubstring("Available Commands"))
		})
	})

	Describe("root --version", func() {
		It("prints version information", func() {
			testApp := createTestApp("", "")
			root := cli.NewRootCmd(testApp)
			cli.SetVersion(root, "1.0.0", "abc123", "2026-03-18")
			root.SetOut(out)
			root.SetErr(out)
			root.SetArgs([]string{"--version"})
			err := root.Execute()
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("flowstate version"))
		})
	})

	Describe("agent list", func() {
		Context("with sample manifests", func() {
			It("prints agents from the agents directory", func() {
				testApp := createTestApp(testdataPath("agents"), "")
				err := cmd(testApp, "agent", "list")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("test-coder"))
				Expect(out.String()).To(ContainSubstring("Test Coder"))
				Expect(out.String()).To(ContainSubstring("standard"))
				Expect(out.String()).To(ContainSubstring("test-researcher"))
			})
		})

		Context("with empty agents directory", func() {
			It("prints no agents found message", func() {
				testApp := createTestApp(testdataPath("empty"), "")
				err := cmd(testApp, "agent", "list")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("No agents found"))
			})
		})
	})

	Describe("agent info", func() {
		It("prints JSON details for a named agent", func() {
			testApp := createTestApp(testdataPath("agents"), "")
			err := cmd(testApp, "agent", "info", "test-coder")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring(`"id": "test-coder"`))
			Expect(out.String()).To(ContainSubstring(`"name": "Test Coder"`))
		})

		It("returns error for unknown agent", func() {
			testApp := createTestApp(testdataPath("agents"), "")
			err := cmd(testApp, "agent", "info", "unknown")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(`agent "unknown" not found`))
		})
	})

	Describe("skill list", func() {
		Context("with sample skills", func() {
			It("prints skills from the skills directory", func() {
				testApp := createTestApp("", testdataPath("skills"))
				err := cmd(testApp, "skill", "list")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("test-skill"))
				Expect(out.String()).To(ContainSubstring("core"))
				Expect(out.String()).To(ContainSubstring("A test skill for unit testing"))
			})
		})

		Context("with empty skills directory", func() {
			It("prints no skills found message", func() {
				testApp := createTestApp("", testdataPath("empty"))
				err := cmd(testApp, "skill", "list")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("No skills found"))
			})
		})
	})

	Describe("discover", func() {
		It("returns suggestions for matching agents", func() {
			testApp := createTestApp(testdataPath("agents"), "")
			err := cmd(testApp, "discover", "write", "code")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("test-coder"))
			Expect(out.String()).To(ContainSubstring("confidence:"))
		})

		It("returns no matching agents message when none match", func() {
			testApp := createTestApp(testdataPath("agents"), "")
			err := cmd(testApp, "discover", "zzzznothing")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("No matching agents found"))
		})
	})

	Describe("session list", func() {
		It("prints placeholder message", func() {
			testApp := createTestApp("", "")
			err := cmd(testApp, "session", "list")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(Equal("No sessions yet.\n"))
		})
	})

	Describe("session resume", func() {
		It("returns error when session not found", func() {
			testApp := createTestApp("", "")
			err := cmd(testApp, "session", "resume", "my-session-123")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(`session "my-session-123" not found`))
		})
	})

	Describe("chat", func() {
		Context("without --message flag", func() {
			It("returns error when engine is not configured", func() {
				testApp := createTestApp("", "")
				err := cmd(testApp, "chat")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("engine not configured"))
			})
		})

		Context("with --message flag", func() {
			It("prints the agent and message with response placeholder when engine is nil", func() {
				testApp := createTestApp("", "")
				err := cmd(testApp, "chat", "--message", "Hello world", "--agent", "test-agent")
				Expect(err).To(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("[test-agent] Hello world"))
			})

			It("prints the agent message with default agent", func() {
				testApp := createTestApp("", "")
				err := cmd(testApp, "chat", "--message", "Hello")
				Expect(err).To(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("[default] Hello"))
			})
		})
	})
})
