package plan_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/plan"
)

// writePlanFile writes a minimal plan markdown file with YAML frontmatter
// to dir. It uses Gomega expectations so failures abort the spec cleanly.
func writePlanFile(dir, id, title, body string) {
	content := "---\n" +
		"id: " + id + "\n" +
		"title: " + title + "\n" +
		"status: draft\n" +
		"---\n\n" +
		body
	Expect(os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0o600)).To(Succeed())
}

// Plan tool tests cover the four plan-related sub-tools (enter, exit, list,
// read) that operate on a directory of plan markdown files. They verify
// metadata reporting, the empty-directory output, listing behaviour, the
// happy path of reading a known plan, and error reporting for missing or
// non-existent ids.
var _ = Describe("Plan tools", func() {
	Describe("Enter / Exit metadata and execution", func() {
		It("reports the correct names", func() {
			Expect(plan.NewEnter().Name()).To(Equal("plan_enter"))
			Expect(plan.NewExit().Name()).To(Equal("plan_exit"))
		})

		It("Enter Execute records action=enter in metadata", func() {
			result, err := plan.NewEnter().Execute(context.Background(), tool.Input{Name: "plan_enter"})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).NotTo(HaveOccurred())
			Expect(result.Metadata).To(HaveKeyWithValue("action", "enter"))
		})
	})

	Describe("List metadata and execution", func() {
		It("reports name, description and an object schema with no required fields", func() {
			lister := plan.NewList(GinkgoT().TempDir())
			Expect(lister.Name()).To(Equal("plan_list"))
			Expect(lister.Description()).NotTo(BeEmpty())
			schema := lister.Schema()
			Expect(schema.Type).To(Equal("object"))
			Expect(schema.Required).To(BeEmpty())
		})

		It("reports 'No plans' for an empty directory", func() {
			dir := GinkgoT().TempDir()
			result, err := plan.NewList(dir).Execute(context.Background(), tool.Input{Name: "plan_list"})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("No plans"))
		})

		It("returns each plan id and title found in the directory", func() {
			dir := GinkgoT().TempDir()
			writePlanFile(dir, "alpha", "Alpha Plan", "# Alpha\n")
			writePlanFile(dir, "beta", "Beta Plan", "# Beta\n")

			result, err := plan.NewList(dir).Execute(context.Background(), tool.Input{Name: "plan_list"})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("alpha"))
			Expect(result.Output).To(ContainSubstring("Alpha Plan"))
			Expect(result.Output).To(ContainSubstring("beta"))
		})
	})

	Describe("Read metadata and execution", func() {
		It("reports name, description and a schema requiring 'id'", func() {
			reader := plan.NewRead(GinkgoT().TempDir())
			Expect(reader.Name()).To(Equal("plan_read"))
			Expect(reader.Description()).NotTo(BeEmpty())
			schema := reader.Schema()
			Expect(schema.Type).To(Equal("object"))
			Expect(schema.Properties).To(HaveKey("id"))
			Expect(schema.Required).To(ContainElement("id"))
		})

		It("returns the plan contents on a successful read", func() {
			dir := GinkgoT().TempDir()
			writePlanFile(dir, "alpha", "Alpha Plan", "# Alpha Plan\n\nSome prose.\n")

			result, err := plan.NewRead(dir).Execute(context.Background(), tool.Input{
				Name:      "plan_read",
				Arguments: map[string]interface{}{"id": "alpha"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("Alpha Plan"))
			Expect(result.Output).To(ContainSubstring("Some prose."))
		})

		It("errors when the id argument is missing", func() {
			dir := GinkgoT().TempDir()
			result, err := plan.NewRead(dir).Execute(context.Background(), tool.Input{
				Name:      "plan_read",
				Arguments: map[string]interface{}{},
			})
			// Either the call returns an error or the result.Error is set —
			// callers should detect either. At least one must be non-nil.
			Expect(err != nil || result.Error != nil).To(BeTrue(),
				"expected error for missing 'id' argument, got nil err and nil result.Error")
		})

		It("includes the requested id and plans dir in the not-found error", func() {
			dir := GinkgoT().TempDir()
			result, err := plan.NewRead(dir).Execute(context.Background(), tool.Input{
				Name:      "plan_read",
				Arguments: map[string]interface{}{"id": "nonexistent"},
			})
			var msg string
			switch {
			case err != nil:
				msg = err.Error()
			case result.Error != nil:
				msg = result.Error.Error()
			default:
				Fail("expected error for missing plan, got nil err and nil result.Error")
			}
			Expect(msg).To(ContainSubstring("nonexistent"))
			Expect(msg).To(ContainSubstring(dir))
		})
	})
})
