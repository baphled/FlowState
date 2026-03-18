package app_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/config"
)

var _ = Describe("App", func() {
	var tempDir string

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "app-test")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("NewForTest", func() {
		It("creates app with minimal configuration", func() {
			tc := app.TestConfig{
				DataDir: tempDir,
			}

			application, err := app.NewForTest(tc)

			Expect(err).NotTo(HaveOccurred())
			Expect(application).NotTo(BeNil())
			Expect(application.Config).NotTo(BeNil())
			Expect(application.Registry).NotTo(BeNil())
			Expect(application.Sessions).NotTo(BeNil())
			Expect(application.Learning).NotTo(BeNil())
			Expect(application.Discovery).NotTo(BeNil())
		})

		It("uses temp directory when DataDir is empty", func() {
			tc := app.TestConfig{}

			application, err := app.NewForTest(tc)

			Expect(err).NotTo(HaveOccurred())
			Expect(application.Config.DataDir).To(Equal(os.TempDir()))
		})

		It("creates sessions directory under DataDir", func() {
			tc := app.TestConfig{
				DataDir: tempDir,
			}

			application, err := app.NewForTest(tc)

			Expect(err).NotTo(HaveOccurred())
			expectedSessionsDir := filepath.Join(tempDir, "sessions")
			Expect(application.SessionsDir()).To(Equal(expectedSessionsDir))
		})

		Context("with agents directory", func() {
			It("discovers agents from directory", func() {
				agentsDir := filepath.Join(tempDir, "agents")
				err := os.MkdirAll(agentsDir, 0o755)
				Expect(err).NotTo(HaveOccurred())

				agentContent := `{"id": "test-agent", "name": "Test Agent"}`
				err = os.WriteFile(filepath.Join(agentsDir, "test.json"), []byte(agentContent), 0o600)
				Expect(err).NotTo(HaveOccurred())

				tc := app.TestConfig{
					DataDir:   tempDir,
					AgentsDir: agentsDir,
				}

				application, err := app.NewForTest(tc)

				Expect(err).NotTo(HaveOccurred())
				agents := application.Registry.List()
				Expect(agents).To(HaveLen(1))
				Expect(agents[0].ID).To(Equal("test-agent"))
			})

			It("returns error for invalid agents directory", func() {
				tc := app.TestConfig{
					DataDir:   tempDir,
					AgentsDir: "/nonexistent/agents",
				}

				application, err := app.NewForTest(tc)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("discovering agents"))
				Expect(application).To(BeNil())
			})
		})

		Context("with skills directory", func() {
			It("loads skills from directory", func() {
				skillsDir := filepath.Join(tempDir, "skills")
				err := os.MkdirAll(skillsDir, 0o755)
				Expect(err).NotTo(HaveOccurred())

				skillDir := filepath.Join(skillsDir, "test-skill")
				err = os.MkdirAll(skillDir, 0o755)
				Expect(err).NotTo(HaveOccurred())

				skillContent := `# Skill: test-skill
Description: A test skill
When to use: Testing purposes
`
				err = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o600)
				Expect(err).NotTo(HaveOccurred())

				tc := app.TestConfig{
					DataDir:   tempDir,
					SkillsDir: skillsDir,
				}

				application, err := app.NewForTest(tc)

				Expect(err).NotTo(HaveOccurred())
				Expect(application.Skills).NotTo(BeEmpty())
			})
		})

		It("sets Engine to nil for test instances", func() {
			tc := app.TestConfig{
				DataDir: tempDir,
			}

			application, err := app.NewForTest(tc)

			Expect(err).NotTo(HaveOccurred())
			Expect(application.Engine).To(BeNil())
		})

		It("sets API to nil for test instances", func() {
			tc := app.TestConfig{
				DataDir: tempDir,
			}

			application, err := app.NewForTest(tc)

			Expect(err).NotTo(HaveOccurred())
			Expect(application.API).To(BeNil())
		})
	})

	Describe("Helper methods", func() {
		var application *app.App

		BeforeEach(func() {
			agentsDir := filepath.Join(tempDir, "agents")
			skillsDir := filepath.Join(tempDir, "skills")
			err := os.MkdirAll(agentsDir, 0o755)
			Expect(err).NotTo(HaveOccurred())
			err = os.MkdirAll(skillsDir, 0o755)
			Expect(err).NotTo(HaveOccurred())

			tc := app.TestConfig{
				DataDir:   tempDir,
				AgentsDir: agentsDir,
				SkillsDir: skillsDir,
			}
			application, err = app.NewForTest(tc)
			Expect(err).NotTo(HaveOccurred())
		})

		Describe("AgentsDir", func() {
			It("returns configured agents directory", func() {
				expectedDir := filepath.Join(tempDir, "agents")

				Expect(application.AgentsDir()).To(Equal(expectedDir))
			})
		})

		Describe("SkillsDir", func() {
			It("returns configured skills directory", func() {
				expectedDir := filepath.Join(tempDir, "skills")

				Expect(application.SkillsDir()).To(Equal(expectedDir))
			})
		})

		Describe("SessionsDir", func() {
			It("returns sessions directory under data dir", func() {
				expectedDir := filepath.Join(tempDir, "sessions")

				Expect(application.SessionsDir()).To(Equal(expectedDir))
			})
		})

		Describe("ConfigPath", func() {
			It("returns config path using ConfigDir()", func() {
				expectedPath := filepath.Join(config.ConfigDir(), "config.yaml")

				Expect(application.ConfigPath()).To(Equal(expectedPath))
			})

			Context("when XDG_CONFIG_HOME is set", func() {
				It("returns config path in XDG_CONFIG_HOME", func() {
					xdgPath := filepath.Join(tempDir, "xdg-config")
					os.Setenv("XDG_CONFIG_HOME", xdgPath)
					DeferCleanup(func() { os.Unsetenv("XDG_CONFIG_HOME") })

					expectedPath := filepath.Join(xdgPath, "flowstate", "config.yaml")

					Expect(application.ConfigPath()).To(Equal(expectedPath))
				})
			})
		})
	})
})
