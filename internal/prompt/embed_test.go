package prompt_test

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/prompt"
)

// canonicalAnchorSentence is the exact substring every prompt that consumes
// tool results must carry. It pairs with the engine-side reminder injected by
// appendToolResultsBatchToMessages so agents anchor on the user's most recent
// user-role message rather than drifting onto tool-result content. See
// internal/prompt/prompts/default-assistant.md (lines 51-61) for the canonical
// "Turn Rules" block this clause originates from, and the parent fix
// `c5595a77 fix(engine,prompt): anchor agent on user prompt after tool-result waves`.
const canonicalAnchorSentence = "Anchor every response on the user's most recent user-role message"

var _ = Describe("Embed", func() {
	Describe("GetPrompt", func() {
		It("returns the default-assistant prompt content", func() {
			content, err := prompt.GetPrompt("default-assistant")
			Expect(err).NotTo(HaveOccurred())
			Expect(content).NotTo(BeEmpty())
			Expect(content).To(ContainSubstring("general-purpose AI assistant"))
		})

		It("returns an error for nonexistent prompt", func() {
			content, err := prompt.GetPrompt("nonexistent")
			Expect(err).To(HaveOccurred())
			Expect(content).To(BeEmpty())
		})
	})

	Describe("HasPrompt", func() {
		It("returns true for default-assistant prompt", func() {
			Expect(prompt.HasPrompt("default-assistant")).To(BeTrue())
		})

		It("returns false for nonexistent prompt", func() {
			Expect(prompt.HasPrompt("nonexistent")).To(BeFalse())
		})
	})

	Describe("ListPrompts", func() {
		It("returns a slice containing default-assistant", func() {
			prompts := prompt.ListPrompts()
			Expect(prompts).To(ContainElement("default-assistant"))
		})

		It("returns a non-empty slice", func() {
			prompts := prompt.ListPrompts()
			Expect(prompts).NotTo(BeEmpty())
		})
	})
})

var _ = Describe("Turn Rules anchor directive propagation", func() {
	// Every prompt that consumes tool results must carry both halves of the
	// Turn Rules guard introduced in `c5595a77`: a "Turn Rules" section header
	// and the canonical anchor sentence. Filesystem-globbing keeps this contract
	// automatic — new prompts get the check for free.

	Describe("internal/prompt/prompts/*.md", func() {
		It("every embedded prompt declares a Turn Rules section", func() {
			for _, path := range listMarkdownFiles("prompts") {
				content := readFile(path)
				Expect(content).To(ContainSubstring("Turn Rules"),
					"prompt missing 'Turn Rules' section: %s", path)
			}
		})

		It("every embedded prompt carries the canonical anchor sentence", func() {
			for _, path := range listMarkdownFiles("prompts") {
				content := readFile(path)
				Expect(content).To(ContainSubstring(canonicalAnchorSentence),
					"prompt missing canonical anchor sentence: %s", path)
			}
		})
	})
})

// listMarkdownFiles returns the absolute paths of every .md file directly
// under dir relative to the package source. Failures fail the calling spec.
func listMarkdownFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	Expect(err).NotTo(HaveOccurred(), "reading %s", dir)
	var paths []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	Expect(paths).NotTo(BeEmpty(), "no .md files found under %s", dir)
	return paths
}

func readFile(path string) string {
	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred(), "reading %s", path)
	return string(data)
}
