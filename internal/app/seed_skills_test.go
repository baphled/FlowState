package app_test

import (
	"io/fs"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/skill"
)

// These specs pin the contract that the four always-active skills the
// user reported missing — pre-action, discipline, skill-discovery,
// agent-discovery — actually reach a runtime that mounts a fresh
// XDG_CONFIG SkillDir.
//
// Background: the FileSkillLoader (internal/skill/loader.go) returns
// the empty slice without error when its base directory does not exist
// (loader.go:44-46). Vanilla `flowstate` invocations resolve cfg.SkillDir
// to ~/.config/flowstate/skills/ via DefaultConfig (config.go:683), and
// that directory is never populated for fresh installs because skills —
// unlike agents (internal/app/embed.go + SeedAgentsDir) and swarms
// (internal/app/embed_swarms.go + SeedSwarmsDir) — lacked the embed +
// seed plumbing. The 33 agent manifests reference these skills, but
// engine.LoadAlwaysActiveSkills returns nil because the loader finds
// no SKILL.md files on disk.
//
// The fix mirrors the agents/swarms pattern: //go:embed skills/*/SKILL.md
// in internal/app/embed_skills.go, EmbeddedSkillsFS() exposing the FS,
// and SeedSkillsDir copying every embedded bundle into cfg.SkillDir on
// startup with skip-on-existing semantics so user edits survive an
// upgrade.
var _ = Describe("EmbeddedSkillsFS", func() {
	It("contains the four user-reported always-active skill bundles", func() {
		fsys := app.EmbeddedSkillsFS()
		Expect(fsys).NotTo(BeNil())

		for _, name := range []string{"pre-action", "discipline", "skill-discovery", "agent-discovery"} {
			path := filepath.Join("skills", name, "SKILL.md")
			data, err := fs.ReadFile(fsys, path)
			Expect(err).NotTo(HaveOccurred(), "embedded FS must carry %s/SKILL.md", name)
			Expect(len(data)).To(BeNumerically(">", 0),
				"embedded SKILL.md for %s must have non-empty content", name)
		}
	})
})

var _ = Describe("SeedSkillsDir", func() {
	var destDir string

	BeforeEach(func() {
		var err error
		destDir, err = os.MkdirTemp("", "seed-skills-test")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(destDir)
	})

	Context("with the embedded skills FS", func() {
		It("seeds the four user-reported always-active skill bundles into a fresh config dir", func() {
			skillsDest := filepath.Join(destDir, "skills")

			err := app.SeedSkillsDir(app.EmbeddedSkillsFS(), skillsDest)
			Expect(err).NotTo(HaveOccurred())

			for _, name := range []string{"pre-action", "discipline", "skill-discovery", "agent-discovery"} {
				path := filepath.Join(skillsDest, name, "SKILL.md")
				Expect(path).To(BeAnExistingFile(),
					"SeedSkillsDir must copy %s/SKILL.md into the destination", name)

				data, err := os.ReadFile(path) //nolint:gosec // path is constructed from a tempdir + a constant
				Expect(err).NotTo(HaveOccurred())
				Expect(len(data)).To(BeNumerically(">", 0),
					"seeded SKILL.md for %s must have non-empty content", name)
			}
		})

		It("preserves user edits — does not overwrite an existing SKILL.md", func() {
			skillsDest := filepath.Join(destDir, "skills")
			preActionDir := filepath.Join(skillsDest, "pre-action")
			Expect(os.MkdirAll(preActionDir, 0o755)).To(Succeed())
			customBody := "---\nname: pre-action\n---\n# my custom override\n"
			Expect(os.WriteFile(filepath.Join(preActionDir, "SKILL.md"), []byte(customBody), 0o600)).To(Succeed())

			err := app.SeedSkillsDir(app.EmbeddedSkillsFS(), skillsDest)
			Expect(err).NotTo(HaveOccurred())

			content, err := os.ReadFile(filepath.Join(preActionDir, "SKILL.md")) //nolint:gosec // tempdir + constant
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(Equal(customBody),
				"SeedSkillsDir must skip existing SKILL.md so user customisations survive an upgrade")
		})
	})
})

// This integration spec exercises the realistic runtime wiring — a fresh
// XDG_CONFIG SkillDir, the binary's embedded skill bundles, and
// engine.LoadAlwaysActiveSkills which is what the agent prompt-build
// path actually calls (see internal/app/app.go:2460 in loadSkills).
//
// On the bug commit (aa8891d) this fails because:
//   1. The skills directory is empty (no embed + seed pipeline).
//   2. FileSkillLoader.LoadAll returns an empty slice without error.
//   3. LoadAlwaysActiveSkills filters that empty slice to nil.
//   4. The agent prompt is built without the four user-named skills.
//
// On the fix it passes because SeedSkillsDir populates the dir from the
// embedded FS before the loader runs, so the four bundles reach the
// agent prompt with their full SKILL.md content.
var _ = Describe("LoadAlwaysActiveSkills with seeded skills dir", func() {
	var skillsDir string

	BeforeEach(func() {
		var err error
		skillsDir, err = os.MkdirTemp("", "always-active-skills-integration")
		Expect(err).NotTo(HaveOccurred())

		Expect(app.SeedSkillsDir(app.EmbeddedSkillsFS(), skillsDir)).To(Succeed())
	})

	AfterEach(func() {
		os.RemoveAll(skillsDir)
	})

	It("delivers the four user-named skills with non-empty content to the engine", func() {
		appLevel := []string{
			"pre-action",
			"memory-keeper",
			"token-cost-estimation",
			"retrospective",
			"note-taking",
			"knowledge-base",
			"discipline",
			"skill-discovery",
			"agent-discovery",
		}

		loaded := engine.LoadAlwaysActiveSkills(skillsDir, appLevel, nil)

		byName := make(map[string]skill.Skill, len(loaded))
		for _, s := range loaded {
			byName[s.Name] = s
		}

		for _, name := range []string{"pre-action", "discipline", "skill-discovery", "agent-discovery"} {
			s, ok := byName[name]
			Expect(ok).To(BeTrue(), "engine.LoadAlwaysActiveSkills must return %s", name)
			Expect(s.Content).NotTo(BeEmpty(),
				"engine.LoadAlwaysActiveSkills must deliver non-empty content for %s — silent empty injection is the bug we are pinning", name)
		}
	})
})
