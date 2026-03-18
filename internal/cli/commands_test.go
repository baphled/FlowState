package cli_test

import (
	"bytes"
	"path/filepath"
	"runtime"

	"github.com/baphled/flowstate/internal/cli"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func testdataPath(subdir string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", subdir)
}

var _ = Describe("CLI Commands", func() {
	var (
		out *bytes.Buffer
		cmd = func(opts *cli.RootOptions, args ...string) error {
			root := cli.NewRootCmdWithOptions(opts)
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
			err := cmd(nil, "--help")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("FlowState provides an AI assistant TUI"))
			Expect(out.String()).To(ContainSubstring("Available Commands"))
		})
	})

	Describe("agent list", func() {
		Context("with sample manifests", func() {
			It("prints agents from the agents directory", func() {
				opts := &cli.RootOptions{
					AgentsDir: testdataPath("agents"),
				}
				err := cmd(opts, "agent", "list")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("test-coder"))
				Expect(out.String()).To(ContainSubstring("Test Coder"))
				Expect(out.String()).To(ContainSubstring("standard"))
				Expect(out.String()).To(ContainSubstring("test-researcher"))
			})
		})

		Context("with empty agents directory", func() {
			It("prints no agents found message", func() {
				opts := &cli.RootOptions{
					AgentsDir: testdataPath("empty"),
				}
				err := cmd(opts, "agent", "list")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("No agents found"))
			})
		})
	})

	Describe("agent info", func() {
		It("prints JSON details for a named agent", func() {
			opts := &cli.RootOptions{
				AgentsDir: testdataPath("agents"),
			}
			err := cmd(opts, "agent", "info", "test-coder")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring(`"id": "test-coder"`))
			Expect(out.String()).To(ContainSubstring(`"name": "Test Coder"`))
		})

		It("returns error for unknown agent", func() {
			opts := &cli.RootOptions{
				AgentsDir: testdataPath("agents"),
			}
			err := cmd(opts, "agent", "info", "unknown")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(`agent "unknown" not found`))
		})
	})

	Describe("skill list", func() {
		Context("with sample skills", func() {
			It("prints skills from the skills directory", func() {
				opts := &cli.RootOptions{
					SkillsDir: testdataPath("skills"),
				}
				err := cmd(opts, "skill", "list")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("test-skill"))
				Expect(out.String()).To(ContainSubstring("core"))
				Expect(out.String()).To(ContainSubstring("A test skill for unit testing"))
			})
		})

		Context("with empty skills directory", func() {
			It("prints no skills found message", func() {
				opts := &cli.RootOptions{
					SkillsDir: testdataPath("empty"),
				}
				err := cmd(opts, "skill", "list")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("No skills found"))
			})
		})
	})

	Describe("discover", func() {
		It("returns suggestions for matching agents", func() {
			opts := &cli.RootOptions{
				AgentsDir: testdataPath("agents"),
			}
			err := cmd(opts, "discover", "write", "code")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("test-coder"))
			Expect(out.String()).To(ContainSubstring("confidence:"))
		})

		It("returns no matching agents message when none match", func() {
			opts := &cli.RootOptions{
				AgentsDir: testdataPath("agents"),
			}
			err := cmd(opts, "discover", "zzzznothing")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("No matching agents found"))
		})
	})

	Describe("session list", func() {
		It("prints placeholder message", func() {
			err := cmd(nil, "session", "list")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(Equal("No sessions yet.\n"))
		})
	})

	Describe("session resume", func() {
		It("prints resuming message with session ID", func() {
			err := cmd(nil, "session", "resume", "my-session-123")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(Equal("Resuming session: my-session-123\n"))
		})
	})

	Describe("chat", func() {
		Context("without --message flag", func() {
			It("prints TUI not wired message", func() {
				err := cmd(nil, "chat")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("TUI not wired yet"))
			})
		})

		Context("with --message flag", func() {
			It("prints the agent and message with placeholder response", func() {
				err := cmd(nil, "chat", "--message", "Hello world", "--agent", "test-agent")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("[test-agent] Hello world"))
				Expect(out.String()).To(ContainSubstring("Response:"))
			})

			It("uses default agent when no agent specified", func() {
				err := cmd(nil, "chat", "--message", "Hello")
				Expect(err).NotTo(HaveOccurred())
				Expect(out.String()).To(ContainSubstring("[default] Hello"))
			})
		})
	})
})
