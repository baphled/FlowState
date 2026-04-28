package engine_test

import (
	"path/filepath"
	"runtime"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/skill"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BuildSystemPrompt skill injection", func() {
	It("injects content for two skills", func() {
		eng := engine.New(engine.Config{
			Skills: []skill.Skill{
				{Name: "pre-action", Content: "PREFLIGHT"},
				{Name: "memory-keeper", Content: "MEMORY"},
			},
		})
		prompt := eng.BuildSystemPrompt()
		Expect(prompt).To(ContainSubstring("# Skill: pre-action"))
		Expect(prompt).To(ContainSubstring("PREFLIGHT"))
		Expect(prompt).To(ContainSubstring("# Skill: memory-keeper"))
		Expect(prompt).To(ContainSubstring("MEMORY"))
	})

	It("injects content for a single skill", func() {
		eng := engine.New(engine.Config{
			Skills: []skill.Skill{
				{Name: "discipline", Content: "DISCIPLINE_CONTENT"},
			},
		})
		prompt := eng.BuildSystemPrompt()
		Expect(prompt).To(ContainSubstring("# Skill: discipline"))
		Expect(prompt).To(ContainSubstring("DISCIPLINE_CONTENT"))
	})

	It("produces no skill marker when Skills is nil", func() {
		eng := engine.New(engine.Config{})
		prompt := eng.BuildSystemPrompt()
		Expect(prompt).NotTo(ContainSubstring("# Skill:"))
	})
})

// These specs pin the program-of-record skill body for autoresearch
// (Slice 2 of the autoresearch plan v3.1). The arbiter sits next to
// the existing skill-injection specs because the contract is the same:
// the engine's skill loader must accept the SKILL.md frontmatter, and
// BuildSystemPrompt must be able to inject the resulting body. The
// content assertions guard the structural anchors required by plan
// § 5.6 — frontmatter shape and the eight prose sections.
var _ = Describe("autoresearch program-of-record skill", func() {
	var sk *skill.Skill

	BeforeEach(func() {
		_, thisFile, _, ok := runtime.Caller(0)
		Expect(ok).To(BeTrue(), "runtime.Caller must resolve test file path")
		repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
		skillPath := filepath.Join(repoRoot, "skills", "autoresearch", "SKILL.md")

		loader := skill.NewFileSkillLoader(filepath.Dir(filepath.Dir(skillPath)))
		var err error
		sk, err = loader.LoadSkill(skillPath)
		Expect(err).NotTo(HaveOccurred(), "skill body must load via FileSkillLoader.LoadSkill")
		Expect(sk).NotTo(BeNil())
	})

	It("declares the canonical frontmatter shape", func() {
		Expect(sk.Name).To(Equal("autoresearch"))
		Expect(sk.Description).NotTo(BeEmpty(), "description is required")
		Expect(sk.Category).NotTo(BeEmpty(), "category is required")
		Expect(sk.Tier).To(Equal(skill.TierDomain))
		Expect(sk.WhenToUse).NotTo(BeEmpty(), "when_to_use is required")
	})

	It("covers every prose section required by plan § 5.6", func() {
		// The plan enumerates eight sections — Goal, Scalar to
		// optimise, Mutable surface constraint, Off-limits surface
		// fields, Trial protocol, Convergence rule, Score-gaming
		// prohibition, What you cannot edit. The body must carry an
		// H2 heading for each so future edits cannot silently drop
		// one.
		body := sk.Content
		Expect(body).To(ContainSubstring("## Goal"))
		Expect(body).To(ContainSubstring("## Scalar to optimise"))
		Expect(body).To(ContainSubstring("## Mutable surface constraint"))
		Expect(body).To(ContainSubstring("## Off-limits surface fields"))
		Expect(body).To(ContainSubstring("## Trial protocol"))
		Expect(body).To(ContainSubstring("## Convergence rule"))
		Expect(body).To(ContainSubstring("## Score-gaming prohibition"))
		Expect(body).To(ContainSubstring("## What you cannot edit"))
	})

	It("derives off-limits manifest fields from the surface, not from a hard-coded list", func() {
		// Plan § 5.6 N4: the off-limits set MUST be computed from
		// the live surface manifest at every trial. The prose must
		// say so explicitly so future edits cannot quietly turn it
		// into a memorised list.
		body := sk.Content
		Expect(body).To(ContainSubstring("Surface-derived"))
		Expect(body).To(ContainSubstring("re-read"))
	})

	It("pins the H1–H5 hypotheses to manifest features for score-gaming guard", func() {
		// Plan § 5.6 N7: the score-gaming section names the
		// validate-harness hypotheses and the manifest features they
		// correspond to. The arbiter checks every hypothesis label
		// is present so a future edit cannot drop one quietly.
		body := sk.Content
		Expect(body).To(ContainSubstring("H1"))
		Expect(body).To(ContainSubstring("H2"))
		Expect(body).To(ContainSubstring("H3"))
		Expect(body).To(ContainSubstring("H4"))
		Expect(body).To(ContainSubstring("H5"))
	})

	It("loads via the engine and is injected by BuildSystemPrompt", func() {
		eng := engine.New(engine.Config{
			Skills: []skill.Skill{*sk},
		})
		prompt := eng.BuildSystemPrompt()
		Expect(prompt).To(ContainSubstring("# Skill: autoresearch"))
		Expect(prompt).To(ContainSubstring("## Trial protocol"))
	})
})
