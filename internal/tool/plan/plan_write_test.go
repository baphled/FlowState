package plan_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/plan"
)

// plan_write closes the regression where plan-writer agents stored plans
// only in coordination_store and never landed them on disk. The tool is
// the agent-facing surface over plan.Store.Create — these specs lock the
// contract: it must accept full markdown with YAML frontmatter, derive
// the filename from the frontmatter's `id`, persist a parseable plan to
// {plansDir}/{id}.md, and reject every malformed-input shape with a
// readable error.
var _ = Describe("plan_write tool", func() {
	var plansDir string

	BeforeEach(func() {
		plansDir = GinkgoT().TempDir()
	})

	It("reports the canonical name", func() {
		Expect(plan.NewWrite(plansDir).Name()).To(Equal("plan_write"))
	})

	It("declares 'markdown' as the single required input", func() {
		s := plan.NewWrite(plansDir).Schema()
		Expect(s.Type).To(Equal("object"))
		Expect(s.Required).To(Equal([]string{"markdown"}))
		Expect(s.Properties).To(HaveKey("markdown"))
	})

	It("persists a plan to disk and surfaces the on-disk path", func() {
		md := "---\n" +
			"id: version-endpoint\n" +
			"title: Add /version endpoint\n" +
			"status: draft\n" +
			"---\n\n" +
			"# Add /version endpoint\n\n" +
			"## Tasks\n\n" +
			"### Task 1: Define response shape\n" +
			"### Task 2: Wire mux handler\n"

		result, err := plan.NewWrite(plansDir).Execute(context.Background(), tool.Input{
			Name:      "plan_write",
			Arguments: map[string]any{"markdown": md},
		})
		Expect(err).NotTo(HaveOccurred())

		expectedPath := filepath.Join(plansDir, "version-endpoint.md")
		Expect(result.Output).To(ContainSubstring(expectedPath),
			"the operator-readable result must include the on-disk path so the user can find the plan")
		Expect(result.Metadata).To(HaveKeyWithValue("path", expectedPath))
		Expect(result.Metadata).To(HaveKeyWithValue("plan_id", "version-endpoint"))

		body, readErr := os.ReadFile(expectedPath)
		Expect(readErr).NotTo(HaveOccurred(),
			"the file at the reported path must exist after Execute returns")
		Expect(string(body)).To(ContainSubstring("id: version-endpoint"))
		Expect(string(body)).To(ContainSubstring("title: Add /version endpoint"))
	})

	It("returns an error when the markdown argument is missing", func() {
		_, err := plan.NewWrite(plansDir).Execute(context.Background(), tool.Input{
			Name:      "plan_write",
			Arguments: map[string]any{},
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("markdown"))
	})

	It("returns an error when the markdown is empty whitespace", func() {
		_, err := plan.NewWrite(plansDir).Execute(context.Background(), tool.Input{
			Name:      "plan_write",
			Arguments: map[string]any{"markdown": "   \n\t\n"},
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("must be a non-empty string"))
	})

	It("returns an error when the YAML frontmatter is missing", func() {
		_, err := plan.NewWrite(plansDir).Execute(context.Background(), tool.Input{
			Name:      "plan_write",
			Arguments: map[string]any{"markdown": "# Just a heading\n\nNo frontmatter."},
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("parsing plan markdown"))
	})

	It("returns an error when the frontmatter has no id field", func() {
		md := "---\n" +
			"title: Has no id\n" +
			"---\n\n# body"
		_, err := plan.NewWrite(plansDir).Execute(context.Background(), tool.Input{
			Name:      "plan_write",
			Arguments: map[string]any{"markdown": md},
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("id"))
	})

	It("rejects ids containing path separators (defence in depth)", func() {
		md := "---\n" +
			"id: ../outside/the-plans-dir\n" +
			"title: bad id\n" +
			"---\n\n# body"
		_, err := plan.NewWrite(plansDir).Execute(context.Background(), tool.Input{
			Name:      "plan_write",
			Arguments: map[string]any{"markdown": md},
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid plan id"))
	})

	It("overwrites an existing plan with the same id (matches plan.Store.Create semantics)", func() {
		md1 := "---\nid: same\ntitle: First version\n---\n\n# v1\n"
		md2 := "---\nid: same\ntitle: Second version\n---\n\n# v2\n"

		w := plan.NewWrite(plansDir)
		_, err := w.Execute(context.Background(), tool.Input{Name: "plan_write", Arguments: map[string]any{"markdown": md1}})
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Execute(context.Background(), tool.Input{Name: "plan_write", Arguments: map[string]any{"markdown": md2}})
		Expect(err).NotTo(HaveOccurred())

		body, readErr := os.ReadFile(filepath.Join(plansDir, "same.md"))
		Expect(readErr).NotTo(HaveOccurred())
		Expect(string(body)).To(ContainSubstring("Second version"),
			"the second write must replace the first — Store.Create is overwrite-on-collision")
		Expect(strings.Count(string(body), "title:")).To(Equal(1),
			"only one frontmatter block — not appended/duplicated")
	})
})
