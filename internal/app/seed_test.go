package app_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
)

var _ = Describe("SeedAgentsDir", func() {
	var (
		destDir string
		srcFS   fs.FS
	)

	BeforeEach(func() {
		var err error
		destDir, err = os.MkdirTemp("", "seed-test")
		Expect(err).NotTo(HaveOccurred())

		srcFS = fstest.MapFS{
			"agents/general.md":    &fstest.MapFile{Data: []byte("---\nid: general\nname: General\n---\n")},
			"agents/coder.md":      &fstest.MapFile{Data: []byte("---\nid: coder\nname: Coder\n---\n")},
			"agents/researcher.md": &fstest.MapFile{Data: []byte("---\nid: researcher\nname: Researcher\n---\n")},
		}
	})

	AfterEach(func() {
		os.RemoveAll(destDir)
	})

	Context("when destination directory is empty", func() {
		It("copies all agent files from source", func() {
			agentsDest := filepath.Join(destDir, "agents")
			Expect(os.MkdirAll(agentsDest, 0o755)).To(Succeed())

			err := app.SeedAgentsDir(srcFS, agentsDest)

			Expect(err).NotTo(HaveOccurred())

			entries, err := os.ReadDir(agentsDest)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(3))

			content, err := os.ReadFile(filepath.Join(agentsDest, "general.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("id: general"))
		})
	})

	Context("when destination directory does not exist", func() {
		It("creates the directory and copies files", func() {
			agentsDest := filepath.Join(destDir, "nonexistent", "agents")

			err := app.SeedAgentsDir(srcFS, agentsDest)

			Expect(err).NotTo(HaveOccurred())

			entries, err := os.ReadDir(agentsDest)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(3))
		})
	})

	Context("when destination directory already has files", func() {
		It("preserves existing files and does not overwrite them", func() {
			agentsDest := filepath.Join(destDir, "agents")
			Expect(os.MkdirAll(agentsDest, 0o755)).To(Succeed())

			customContent := "---\nid: general\nname: My Custom General\n---\n"
			Expect(os.WriteFile(filepath.Join(agentsDest, "general.md"), []byte(customContent), 0o600)).To(Succeed())

			err := app.SeedAgentsDir(srcFS, agentsDest)

			Expect(err).NotTo(HaveOccurred())

			content, err := os.ReadFile(filepath.Join(agentsDest, "general.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("My Custom General"))
		})

		It("copies missing files while preserving existing ones", func() {
			agentsDest := filepath.Join(destDir, "agents")
			Expect(os.MkdirAll(agentsDest, 0o755)).To(Succeed())

			customContent := "---\nid: general\nname: My Custom General\n---\n"
			Expect(os.WriteFile(filepath.Join(agentsDest, "general.md"), []byte(customContent), 0o600)).To(Succeed())

			err := app.SeedAgentsDir(srcFS, agentsDest)

			Expect(err).NotTo(HaveOccurred())

			entries, err := os.ReadDir(agentsDest)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(3))

			content, err := os.ReadFile(filepath.Join(agentsDest, "general.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("My Custom General"))

			content, err = os.ReadFile(filepath.Join(agentsDest, "coder.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("id: coder"))
		})

		It("preserves a custom agent file that has no embedded counterpart", func() {
			agentsDest := filepath.Join(destDir, "agents")
			Expect(os.MkdirAll(agentsDest, 0o755)).To(Succeed())

			customContent := "---\nid: my-custom\nname: My Custom Agent\n---\n"
			Expect(os.WriteFile(filepath.Join(agentsDest, "my-custom.md"), []byte(customContent), 0o600)).To(Succeed())

			err := app.SeedAgentsDir(srcFS, agentsDest)

			Expect(err).NotTo(HaveOccurred())

			content, err := os.ReadFile(filepath.Join(agentsDest, "my-custom.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("My Custom Agent"))
		})
	})

	Context("when source FS has no agents directory", func() {
		It("returns an error", func() {
			emptyFS := fstest.MapFS{}
			agentsDest := filepath.Join(destDir, "agents")

			err := app.SeedAgentsDir(emptyFS, agentsDest)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("agents"))
		})
	})
})

var _ = Describe("RefreshAgentManifests", func() {
	var (
		destDir string
		srcFS   fs.FS
	)

	BeforeEach(func() {
		var err error
		destDir, err = os.MkdirTemp("", "refresh-test")
		Expect(err).NotTo(HaveOccurred())

		srcFS = fstest.MapFS{
			"agents/general.md":    &fstest.MapFile{Data: []byte("---\nid: general\nname: General v2\n---\n")},
			"agents/coder.md":      &fstest.MapFile{Data: []byte("---\nid: coder\nname: Coder v2\n---\n")},
			"agents/researcher.md": &fstest.MapFile{Data: []byte("---\nid: researcher\nname: Researcher v2\n---\n")},
		}
	})

	AfterEach(func() {
		os.RemoveAll(destDir)
	})

	Context("when an existing manifest differs from the embedded version", func() {
		It("overwrites the stale file and reports it as updated", func() {
			staleContent := "---\nid: general\nname: General v1 (stale)\n---\n"
			Expect(os.WriteFile(filepath.Join(destDir, "general.md"), []byte(staleContent), 0o600)).To(Succeed())

			report, err := app.RefreshAgentManifests(srcFS, destDir, app.RefreshOptions{})

			Expect(err).NotTo(HaveOccurred())

			content, err := os.ReadFile(filepath.Join(destDir, "general.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("General v2"))
			Expect(string(content)).NotTo(ContainSubstring("stale"))

			entry := findEntry(report, "general.md")
			Expect(entry).NotTo(BeNil())
			Expect(entry.Status).To(Equal(app.RefreshStatusUpdated))
		})
	})

	Context("when an existing manifest matches the embedded version byte-for-byte", func() {
		It("leaves the file untouched and reports it as unchanged", func() {
			identical := "---\nid: general\nname: General v2\n---\n"
			destPath := filepath.Join(destDir, "general.md")
			Expect(os.WriteFile(destPath, []byte(identical), 0o600)).To(Succeed())

			priorStat, err := os.Stat(destPath)
			Expect(err).NotTo(HaveOccurred())

			report, err := app.RefreshAgentManifests(srcFS, destDir, app.RefreshOptions{})

			Expect(err).NotTo(HaveOccurred())

			postStat, err := os.Stat(destPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(postStat.ModTime()).To(Equal(priorStat.ModTime()))

			entry := findEntry(report, "general.md")
			Expect(entry).NotTo(BeNil())
			Expect(entry.Status).To(Equal(app.RefreshStatusUnchanged))
		})
	})

	Context("when the destination file does not exist", func() {
		It("creates the file and reports it as created", func() {
			report, err := app.RefreshAgentManifests(srcFS, destDir, app.RefreshOptions{})

			Expect(err).NotTo(HaveOccurred())

			content, err := os.ReadFile(filepath.Join(destDir, "general.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("General v2"))

			entry := findEntry(report, "general.md")
			Expect(entry).NotTo(BeNil())
			Expect(entry.Status).To(Equal(app.RefreshStatusCreated))
		})
	})

	Context("when dry-run is enabled", func() {
		It("writes nothing but still reports what would change", func() {
			staleContent := "---\nid: general\nname: General v1 (stale)\n---\n"
			Expect(os.WriteFile(filepath.Join(destDir, "general.md"), []byte(staleContent), 0o600)).To(Succeed())

			report, err := app.RefreshAgentManifests(srcFS, destDir, app.RefreshOptions{DryRun: true})

			Expect(err).NotTo(HaveOccurred())

			content, err := os.ReadFile(filepath.Join(destDir, "general.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("stale"), "dry-run must not modify the file")

			entry := findEntry(report, "general.md")
			Expect(entry).NotTo(BeNil())
			Expect(entry.Status).To(Equal(app.RefreshStatusUpdated))
		})

		It("does not create missing files", func() {
			report, err := app.RefreshAgentManifests(srcFS, destDir, app.RefreshOptions{DryRun: true})

			Expect(err).NotTo(HaveOccurred())

			_, statErr := os.Stat(filepath.Join(destDir, "general.md"))
			Expect(os.IsNotExist(statErr)).To(BeTrue(), "dry-run must not create the file")

			entry := findEntry(report, "general.md")
			Expect(entry).NotTo(BeNil())
			Expect(entry.Status).To(Equal(app.RefreshStatusCreated))
		})
	})

	Context("when an OnlyAgent filter is set", func() {
		It("only touches the matching manifest", func() {
			staleOne := "---\nid: general\nname: stale one\n---\n"
			staleTwo := "---\nid: coder\nname: stale two\n---\n"
			Expect(os.WriteFile(filepath.Join(destDir, "general.md"), []byte(staleOne), 0o600)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(destDir, "coder.md"), []byte(staleTwo), 0o600)).To(Succeed())

			report, err := app.RefreshAgentManifests(srcFS, destDir, app.RefreshOptions{OnlyAgent: "general"})

			Expect(err).NotTo(HaveOccurred())

			generalContent, err := os.ReadFile(filepath.Join(destDir, "general.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(generalContent)).To(ContainSubstring("General v2"))

			coderContent, err := os.ReadFile(filepath.Join(destDir, "coder.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(coderContent)).To(ContainSubstring("stale two"))

			Expect(report).To(HaveLen(1))
			Expect(report[0].Name).To(Equal("general.md"))
		})

		It("returns an error when no manifest matches", func() {
			_, err := app.RefreshAgentManifests(srcFS, destDir, app.RefreshOptions{OnlyAgent: "nonexistent"})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("nonexistent"))
		})
	})
})

// findEntry returns the RefreshEntry matching name, or nil if absent.
//
// Expected:
//   - report is the slice returned by RefreshAgentManifests.
//   - name is a manifest filename such as "general.md".
//
// Returns:
//   - A pointer to the first matching entry, or nil.
//
// Side effects:
//   - None.
func findEntry(report app.RefreshReport, name string) *app.RefreshEntry { //nolint:unparam // helper is generic; current specs all query "general.md" but the parameter documents intent for future tests
	for i := range report {
		if report[i].Name == name {
			return &report[i]
		}
	}
	return nil
}

var _ = Describe("MigrateAgentsToConfigDir", func() {
	var (
		root   string
		oldDir string
		newDir string
	)

	BeforeEach(func() {
		var err error
		root, err = os.MkdirTemp("", "agents-migrate-test")
		Expect(err).NotTo(HaveOccurred())
		oldDir = filepath.Join(root, "xdg-data", "flowstate", "agents")
		newDir = filepath.Join(root, "xdg-config", "flowstate", "agents")
	})

	AfterEach(func() {
		os.RemoveAll(root)
	})

	Context("when only the legacy XDG_DATA dir has manifests", func() {
		It("copies them into the new XDG_CONFIG dir and reports migrated", func() {
			Expect(os.MkdirAll(oldDir, 0o755)).To(Succeed())
			plannerBody := "---\nid: planner\nname: User Planner\n---\n"
			Expect(os.WriteFile(filepath.Join(oldDir, "planner.md"), []byte(plannerBody), 0o600)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(oldDir, "executor.md"), []byte("executor body"), 0o600)).To(Succeed())

			result, err := app.MigrateAgentsToConfigDir(oldDir, newDir)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(app.MigrateAgentsResultMigrated))

			content, err := os.ReadFile(filepath.Join(newDir, "planner.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(Equal(plannerBody))

			Expect(filepath.Join(newDir, "executor.md")).To(BeAnExistingFile())
		})

		It("leaves the legacy directory intact so the user can review and remove it", func() {
			Expect(os.MkdirAll(oldDir, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(oldDir, "planner.md"), []byte("body"), 0o600)).To(Succeed())

			_, err := app.MigrateAgentsToConfigDir(oldDir, newDir)
			Expect(err).NotTo(HaveOccurred())

			Expect(filepath.Join(oldDir, "planner.md")).To(BeAnExistingFile(),
				"migration must COPY, not MOVE — the legacy file must remain")
		})

		It("ignores non-.md entries and subdirectories in the legacy directory", func() {
			Expect(os.MkdirAll(oldDir, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(oldDir, "planner.md"), []byte("body"), 0o600)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(oldDir, "README.txt"), []byte("notes"), 0o600)).To(Succeed())
			Expect(os.MkdirAll(filepath.Join(oldDir, "subdir"), 0o755)).To(Succeed())

			result, err := app.MigrateAgentsToConfigDir(oldDir, newDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(app.MigrateAgentsResultMigrated))

			Expect(filepath.Join(newDir, "planner.md")).To(BeAnExistingFile())
			_, statErr := os.Stat(filepath.Join(newDir, "README.txt"))
			Expect(os.IsNotExist(statErr)).To(BeTrue())
		})
	})

	Context("when both XDG_DATA and XDG_CONFIG agent dirs already exist", func() {
		It("prefers XDG_CONFIG and skips the migration without overwriting", func() {
			Expect(os.MkdirAll(oldDir, 0o755)).To(Succeed())
			Expect(os.MkdirAll(newDir, 0o755)).To(Succeed())
			canonical := "---\nid: planner\nname: Canonical\n---\n"
			Expect(os.WriteFile(filepath.Join(newDir, "planner.md"), []byte(canonical), 0o600)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(oldDir, "planner.md"), []byte("legacy"), 0o600)).To(Succeed())

			result, err := app.MigrateAgentsToConfigDir(oldDir, newDir)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(app.MigrateAgentsResultSkippedNewExists))

			content, err := os.ReadFile(filepath.Join(newDir, "planner.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(Equal(canonical), "XDG_CONFIG copy must remain untouched")
		})
	})

	Context("when only XDG_CONFIG exists and XDG_DATA is missing", func() {
		It("is a no-op and reports skipped-new-exists", func() {
			Expect(os.MkdirAll(newDir, 0o755)).To(Succeed())
			canonical := "---\nid: planner\nname: Canonical\n---\n"
			Expect(os.WriteFile(filepath.Join(newDir, "planner.md"), []byte(canonical), 0o600)).To(Succeed())

			result, err := app.MigrateAgentsToConfigDir(oldDir, newDir)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(app.MigrateAgentsResultSkippedNewExists))
		})
	})

	Context("when neither directory exists", func() {
		It("reports skipped-no-legacy without creating anything", func() {
			result, err := app.MigrateAgentsToConfigDir(oldDir, newDir)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(app.MigrateAgentsResultSkippedNoLegacy))

			_, statErr := os.Stat(newDir)
			Expect(os.IsNotExist(statErr)).To(BeTrue())
		})
	})

	Context("when the legacy directory exists but contains no .md files", func() {
		It("reports skipped-no-legacy and does not create the new directory", func() {
			Expect(os.MkdirAll(oldDir, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(oldDir, "README.txt"), []byte("notes"), 0o600)).To(Succeed())

			result, err := app.MigrateAgentsToConfigDir(oldDir, newDir)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(app.MigrateAgentsResultSkippedNoLegacy))

			_, statErr := os.Stat(newDir)
			Expect(os.IsNotExist(statErr)).To(BeTrue())
		})
	})
})

var _ = Describe("MigrateSkillsToConfigDir", func() {
	var (
		root   string
		oldDir string
		newDir string
	)

	BeforeEach(func() {
		var err error
		root, err = os.MkdirTemp("", "skills-migrate-test")
		Expect(err).NotTo(HaveOccurred())
		oldDir = filepath.Join(root, "xdg-data", "flowstate", "skills")
		newDir = filepath.Join(root, "xdg-config", "flowstate", "skills")
	})

	AfterEach(func() {
		os.RemoveAll(root)
	})

	Context("when only the legacy XDG_DATA dir has bundles", func() {
		It("copies them into the new XDG_CONFIG dir and reports migrated", func() {
			writeSkillBundle(oldDir, "pre-action", "---\nname: pre-action\n---\n# Pre Action\n")
			writeSkillBundle(oldDir, "discipline", "---\nname: discipline\n---\n# Discipline\n")

			result, err := app.MigrateSkillsToConfigDir(oldDir, newDir)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(app.MigrateSkillsResultMigrated))

			content, err := os.ReadFile(filepath.Join(newDir, "pre-action", "SKILL.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("Pre Action"))

			Expect(filepath.Join(newDir, "discipline", "SKILL.md")).To(BeAnExistingFile())
		})

		It("preserves nested files inside a skill bundle", func() {
			writeSkillBundle(oldDir, "research", "---\nname: research\n---\nbody")
			Expect(os.WriteFile(filepath.Join(oldDir, "research", "reference.md"), []byte("ref"), 0o600)).To(Succeed())
			Expect(os.MkdirAll(filepath.Join(oldDir, "research", "examples"), 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(oldDir, "research", "examples", "one.md"), []byte("ex1"), 0o600)).To(Succeed())

			result, err := app.MigrateSkillsToConfigDir(oldDir, newDir)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(app.MigrateSkillsResultMigrated))

			Expect(filepath.Join(newDir, "research", "reference.md")).To(BeAnExistingFile())
			Expect(filepath.Join(newDir, "research", "examples", "one.md")).To(BeAnExistingFile())
		})

		It("leaves the legacy directory intact so the user can review and remove it", func() {
			writeSkillBundle(oldDir, "pre-action", "body")

			_, err := app.MigrateSkillsToConfigDir(oldDir, newDir)
			Expect(err).NotTo(HaveOccurred())

			Expect(filepath.Join(oldDir, "pre-action", "SKILL.md")).To(BeAnExistingFile(),
				"migration must COPY, not MOVE — the legacy bundle must remain")
		})

		It("ignores subdirectories that have no SKILL.md and loose files", func() {
			writeSkillBundle(oldDir, "pre-action", "body")
			Expect(os.MkdirAll(filepath.Join(oldDir, "not-a-skill"), 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(oldDir, "not-a-skill", "notes.md"), []byte("notes"), 0o600)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(oldDir, "README.txt"), []byte("readme"), 0o600)).To(Succeed())

			result, err := app.MigrateSkillsToConfigDir(oldDir, newDir)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(app.MigrateSkillsResultMigrated))

			Expect(filepath.Join(newDir, "pre-action", "SKILL.md")).To(BeAnExistingFile())
			_, statErr := os.Stat(filepath.Join(newDir, "not-a-skill"))
			Expect(os.IsNotExist(statErr)).To(BeTrue())
			_, statErr = os.Stat(filepath.Join(newDir, "README.txt"))
			Expect(os.IsNotExist(statErr)).To(BeTrue())
		})
	})

	Context("when both XDG_DATA and XDG_CONFIG skill dirs already exist", func() {
		It("prefers XDG_CONFIG and skips the migration without overwriting", func() {
			writeSkillBundle(oldDir, "pre-action", "legacy body")
			canonical := "---\nname: pre-action\n---\ncanonical body\n"
			writeSkillBundle(newDir, "pre-action", canonical)

			result, err := app.MigrateSkillsToConfigDir(oldDir, newDir)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(app.MigrateSkillsResultSkippedNewExists))

			content, err := os.ReadFile(filepath.Join(newDir, "pre-action", "SKILL.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(Equal(canonical), "XDG_CONFIG copy must remain untouched")
		})
	})

	Context("when only XDG_CONFIG exists and XDG_DATA is missing", func() {
		It("is a no-op and reports skipped-new-exists", func() {
			canonical := "---\nname: pre-action\n---\ncanonical body\n"
			writeSkillBundle(newDir, "pre-action", canonical)

			result, err := app.MigrateSkillsToConfigDir(oldDir, newDir)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(app.MigrateSkillsResultSkippedNewExists))
		})
	})

	Context("when neither directory exists", func() {
		It("reports skipped-no-legacy without creating anything", func() {
			result, err := app.MigrateSkillsToConfigDir(oldDir, newDir)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(app.MigrateSkillsResultSkippedNoLegacy))

			_, statErr := os.Stat(newDir)
			Expect(os.IsNotExist(statErr)).To(BeTrue())
		})
	})

	Context("when the legacy directory exists but contains no skill bundles", func() {
		It("reports skipped-no-legacy and does not create the new directory", func() {
			Expect(os.MkdirAll(oldDir, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(oldDir, "README.txt"), []byte("notes"), 0o600)).To(Succeed())
			Expect(os.MkdirAll(filepath.Join(oldDir, "empty-bundle"), 0o755)).To(Succeed())

			result, err := app.MigrateSkillsToConfigDir(oldDir, newDir)

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(app.MigrateSkillsResultSkippedNoLegacy))

			_, statErr := os.Stat(newDir)
			Expect(os.IsNotExist(statErr)).To(BeTrue())
		})
	})
})

// writeSkillBundle creates a skill bundle directory at <root>/<name> and
// writes the provided body to its SKILL.md. Used by MigrateSkillsToConfigDir
// specs to set up legacy and canonical fixtures with one line per bundle.
//
// Expected:
//   - root is the absolute path of the skills directory.
//   - name is the bundle directory name (no slashes).
//   - body is the SKILL.md contents.
//
// Side effects:
//   - Creates the bundle directory and writes SKILL.md.
func writeSkillBundle(root, name, body string) {
	dir := filepath.Join(root, name)
	Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600)).To(Succeed())
}

var _ = Describe("SeedSwarmsDir", func() {
	var (
		destDir string
		srcFS   fs.FS
	)

	BeforeEach(func() {
		var err error
		destDir, err = os.MkdirTemp("", "seed-swarms-test")
		Expect(err).NotTo(HaveOccurred())

		srcFS = fstest.MapFS{
			"swarms/planning-loop.yml": &fstest.MapFile{Data: []byte("schema_version: \"1.0.0\"\nid: planning-loop\nlead: planner\nmembers: []\n")},
			"swarms/solo.yml":          &fstest.MapFile{Data: []byte("schema_version: \"1.0.0\"\nid: solo\nlead: executor\nmembers: []\n")},
		}
	})

	AfterEach(func() {
		os.RemoveAll(destDir)
	})

	Context("when destination directory is empty", func() {
		It("copies all swarm files from source", func() {
			swarmsDest := filepath.Join(destDir, "swarms")
			Expect(os.MkdirAll(swarmsDest, 0o755)).To(Succeed())

			err := app.SeedSwarmsDir(srcFS, swarmsDest)

			Expect(err).NotTo(HaveOccurred())
			entries, err := os.ReadDir(swarmsDest)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(2))

			content, err := os.ReadFile(filepath.Join(swarmsDest, "planning-loop.yml"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("id: planning-loop"))
		})
	})

	Context("when destination directory does not exist", func() {
		It("creates the directory and copies files", func() {
			swarmsDest := filepath.Join(destDir, "nonexistent", "swarms")

			err := app.SeedSwarmsDir(srcFS, swarmsDest)

			Expect(err).NotTo(HaveOccurred())
			entries, err := os.ReadDir(swarmsDest)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(2))
		})
	})

	Context("when destination directory already has files", func() {
		It("preserves existing files and does not overwrite them", func() {
			swarmsDest := filepath.Join(destDir, "swarms")
			Expect(os.MkdirAll(swarmsDest, 0o755)).To(Succeed())

			customContent := "schema_version: \"1.0.0\"\nid: planning-loop\nlead: my-custom-planner\nmembers: []\n"
			Expect(os.WriteFile(filepath.Join(swarmsDest, "planning-loop.yml"), []byte(customContent), 0o600)).To(Succeed())

			err := app.SeedSwarmsDir(srcFS, swarmsDest)

			Expect(err).NotTo(HaveOccurred())
			content, err := os.ReadFile(filepath.Join(swarmsDest, "planning-loop.yml"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("my-custom-planner"))
		})

		It("preserves a custom swarm file with no embedded counterpart", func() {
			swarmsDest := filepath.Join(destDir, "swarms")
			Expect(os.MkdirAll(swarmsDest, 0o755)).To(Succeed())

			customContent := "schema_version: \"1.0.0\"\nid: my-team\nlead: planner\nmembers: []\n"
			Expect(os.WriteFile(filepath.Join(swarmsDest, "my-team.yml"), []byte(customContent), 0o600)).To(Succeed())

			err := app.SeedSwarmsDir(srcFS, swarmsDest)

			Expect(err).NotTo(HaveOccurred())
			content, err := os.ReadFile(filepath.Join(swarmsDest, "my-team.yml"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("id: my-team"))
		})
	})

	Context("when source FS has no swarms directory", func() {
		It("returns an error", func() {
			emptyFS := fstest.MapFS{}
			swarmsDest := filepath.Join(destDir, "swarms")

			err := app.SeedSwarmsDir(emptyFS, swarmsDest)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("swarms"))
		})
	})

	Context("with the embedded swarms FS", func() {
		It("seeds planning-loop.yml and solo.yml on first run", func() {
			swarmsDest := filepath.Join(destDir, "swarms")

			err := app.SeedSwarmsDir(app.EmbeddedSwarmsFS(), swarmsDest)

			Expect(err).NotTo(HaveOccurred())
			entries, err := os.ReadDir(swarmsDest)
			Expect(err).NotTo(HaveOccurred())
			names := make([]string, 0, len(entries))
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			Expect(names).To(ContainElements("planning-loop.yml", "solo.yml"))
		})
	})
})

var _ = Describe("EmbeddedAgentsFS", func() {
	Context("when calling EmbeddedAgentsFS", func() {
		It("returns a valid fs.FS", func() {
			embeddedFS := app.EmbeddedAgentsFS()

			Expect(embeddedFS).NotTo(BeNil())
		})

		It("contains planner.md", func() {
			embeddedFS := app.EmbeddedAgentsFS()

			agentsDir, err := fs.Sub(embeddedFS, "agents")
			Expect(err).NotTo(HaveOccurred())

			plannerContent, err := fs.ReadFile(agentsDir, "planner.md")
			Expect(err).NotTo(HaveOccurred())
			Expect(string(plannerContent)).To(ContainSubstring("id: planner"))
		})

		It("contains executor.md", func() {
			embeddedFS := app.EmbeddedAgentsFS()

			agentsDir, err := fs.Sub(embeddedFS, "agents")
			Expect(err).NotTo(HaveOccurred())

			executorContent, err := fs.ReadFile(agentsDir, "executor.md")
			Expect(err).NotTo(HaveOccurred())
			Expect(string(executorContent)).To(ContainSubstring("id: executor"))
		})

		It("can seed all manifests on first run", func() {
			destDir, err := os.MkdirTemp("", "embedded-seed-test")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(destDir) })

			embeddedFS := app.EmbeddedAgentsFS()
			err = app.SeedAgentsDir(embeddedFS, destDir)

			Expect(err).NotTo(HaveOccurred())

			entries, err := os.ReadDir(destDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(embeddedAgentCount()))

			plannerContent, err := os.ReadFile(filepath.Join(destDir, "planner.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(plannerContent)).To(ContainSubstring("id: planner"))

			executorContent, err := os.ReadFile(filepath.Join(destDir, "executor.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(executorContent)).To(ContainSubstring("id: executor"))
		})
	})
})
