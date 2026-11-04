package skill_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/skill"
)

var _ = Describe("Skill loader", func() {
	var dir string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		body := "---\nname: demo\ndescription: demo skill\n---\n\n## Guidance\nDo the thing.\n"
		writeErr := os.WriteFile(filepath.Join(dir, "demo.md"), []byte(body), 0o600)
		Expect(writeErr).NotTo(HaveOccurred())
	})

	It("loads every markdown skill in a directory", func() {
		skills, err := skill.LoadDir(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(skills["demo"].Description).To(Equal("demo skill"))
	})

	It("rejects a skill file without frontmatter", func() {
		_, err := skill.ParseSkill([]byte("no frontmatter here"))
		Expect(err).To(MatchError(ContainSubstring("frontmatter")))
	})
})

var _ = Describe("Hook runner", func() {
	It("fires the command bound to a matching event", func() {
		r := skill.NewRunner(skill.Hook{Event: skill.EventPacketAdmitted, Subject: "p1", Name: "true"})
		errs := r.Fire(skill.EventPacketAdmitted, "p1")
		Expect(errs).To(BeEmpty())
	})

	It("skips hooks bound to a different subject", func() {
		r := skill.NewRunner(skill.Hook{Event: skill.EventPacketAdmitted, Subject: "p1", Name: "false"})
		r.Fire(skill.EventPacketAdmitted, "p2")
		Expect(r.Fired()).To(BeEmpty())
	})

	It("reports an unknown event as an error", func() {
		r := skill.NewRunner()
		errs := r.Fire(skill.HookEvent("nonsense"), "p1")
		Expect(errs).To(HaveLen(1))
	})

	It("collects command failures without aborting later hooks", func() {
		r := skill.NewRunner(
			skill.Hook{Event: skill.EventGateFailed, Name: "false"},
			skill.Hook{Event: skill.EventGateFailed, Name: "true"})
		errs := r.Fire(skill.EventGateFailed, "go")
		Expect(errs).To(HaveLen(1))
	})
})
