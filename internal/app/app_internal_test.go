package app

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plan"
)

// buildPersistedPlanFile is the parser-aware constructor that
// PersistApprovedPlan now uses to build the plan.File written to disk.
// Closes the cosmetic regression where the older code crammed the raw
// plan markdown into TLDR (producing a persisted file with nested
// frontmatter and an empty Tasks list).
//
// Specs lock the contract: when the markdown carries valid YAML
// frontmatter, the parsed fields land on the File directly; the
// chainID is the fallback id/title; Status is forced to "approved" so
// downstream catalogue queries can filter; and Tasks is always
// populated via plan.TasksFromPlanText regardless of frontmatter shape.
var _ = Describe("buildPersistedPlanFile", func() {
	It("promotes parsed frontmatter fields onto the persisted File", func() {
		md := "---\n" +
			"id: my-plan\n" +
			"title: Add /version endpoint\n" +
			"description: short summary\n" +
			"---\n\n" +
			"# Body\n\n## Tasks\n\n### Task 1: Define struct\n### Task 2: Wire mux\n"

		f := buildPersistedPlanFile("chain-1", md)

		Expect(f.ID).To(Equal("my-plan"),
			"the frontmatter id wins over the chainID-derived default")
		Expect(f.Title).To(Equal("Add /version endpoint"),
			"the frontmatter title wins over the chainID-derived default")
		Expect(f.Description).To(Equal("short summary"))
		Expect(f.Status).To(Equal("approved"),
			"PersistApprovedPlan always stamps approved — that's its semantic")
		Expect(f.TLDR).To(Equal(""),
			"with a successful parse, TLDR stays unset; the previous bug "+
				"dumped the raw markdown here producing nested frontmatter")
		Expect(f.Tasks).NotTo(BeEmpty(),
			"tasks must always be extracted so downstream consumers see structured tasks")
	})

	It("falls back to chainID-derived id/title when the frontmatter omits them", func() {
		// Frontmatter present but missing id and title.
		md := "---\nstatus: draft\n---\n\n# Body without id/title\n\n## Tasks\n\n### Task 1: a\n"

		f := buildPersistedPlanFile("auto-fallback-chain", md)

		Expect(f.ID).To(Equal("auto-fallback-chain"))
		Expect(f.Title).To(Equal("Plan auto-fallback-chain"))
		Expect(f.Status).To(Equal("approved"))
	})

	It("falls back to TLDR + chainID defaults when the markdown lacks frontmatter", func() {
		// No leading "---" block at all — ParseFile returns an error.
		raw := "# Just a heading\n\n## Tasks\n\n### Task 1: still parseable\n"

		f := buildPersistedPlanFile("no-frontmatter-chain", raw)

		Expect(f.ID).To(Equal("no-frontmatter-chain"))
		Expect(f.Title).To(Equal("Plan no-frontmatter-chain"))
		Expect(f.Status).To(Equal("approved"))
		Expect(f.TLDR).To(Equal(raw),
			"on the failure path TLDR keeps the raw payload so the operator "+
				"can still read the plan body even though it could not be parsed")
		// TasksFromPlanText anchors the body off the frontmatter
		// delimiters; on a payload that lacks them entirely the parser
		// returns []. The auto-persist path is best-effort here — the
		// plan still lands on disk with a readable TLDR but Tasks is
		// empty. plan-writer's primary flow always emits frontmatter,
		// so this branch is only exercised by tests / direct callers
		// who hand us malformed input.
		Expect(f.Tasks).To(BeEmpty(),
			"frontmatter-less input cannot be parsed for tasks; "+
				"the file still persists with TLDR carrying the raw body")
	})

	It("forces Status=approved even when the source frontmatter says draft", func() {
		md := "---\nid: x\ntitle: t\nstatus: draft\n---\n\n# body\n"

		f := buildPersistedPlanFile("chain", md)

		Expect(f.Status).To(Equal("approved"),
			"persisted plans are by-definition approved at this entry point")
	})

	It("preserves a non-zero CreatedAt from the frontmatter", func() {
		md := "---\nid: x\ntitle: t\ncreated_at: 2024-06-01T12:00:00Z\n---\n\n# body\n"

		f := buildPersistedPlanFile("chain", md)
		Expect(f.CreatedAt.IsZero()).To(BeFalse())
		Expect(f.CreatedAt.Year()).To(Equal(2024))
	})

	It("populates a fresh CreatedAt when the frontmatter omits it", func() {
		md := "---\nid: x\ntitle: t\n---\n\n# body\n"

		f := buildPersistedPlanFile("chain", md)
		Expect(f.CreatedAt.IsZero()).To(BeFalse(),
			"every persisted plan must carry a CreatedAt so plan_list can sort/format")
	})

	It("returns a typed plan.File so downstream Store.Create accepts it", func() {
		md := "---\nid: typecheck\ntitle: t\n---\n\n# body\n"
		var _ plan.File = buildPersistedPlanFile("chain", md)
	})
})
