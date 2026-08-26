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

// todoDisciplineHeader is the section marker that flags the prompt carries the
// universal todo-list discipline. Sibling to "Turn Rules" — both are always-on
// behavioural guards independent of the tool loop.
const todoDisciplineHeader = "Todo Discipline"

// canonicalTodoMandate is the exact substring every prompt that handles
// multi-step work MUST carry. The clause is intentionally identical across
// prompts so the discipline is consistent regardless of which agent answers.
// Names the actual tool (`todowrite`, registered at internal/tool/todo/todo.go)
// so agents have no ambiguity about which tool to call. Pairs with session
// `089c7cd5-37d8-4a59-868d-366d2dca0cfb` where default-assistant ran six
// assistant turns without ever creating a todo list despite an explicit user
// instruction at index 0.
const canonicalTodoMandate = "Always use the `todowrite` tool to track multi-step work; do not start work on a multi-step task without first recording it."

// canonicalAutoContinueSentence is the exact substring every prompt that
// carries the Todo Discipline block MUST add — it forbids mid-list permission
// asking. User feedback (May 2026): agents were pausing mid-todo-list to ask
// "should I continue?" or waiting for an additional prompt before working on
// the next item. Direct quote: "If the agent has tasks to do, unless it needs
// my input, it should just continue with the work, and not ask for me to
// continue, or add another prompt." This clause names the three legitimate
// pause reasons (missing input, unresolvable blocker, list completion) and the
// three anti-pattern phrasings so every agent has the same explicit guard.
const canonicalAutoContinueSentence = `Once the list is recorded, work through it without asking the user "should I continue?", "do you want me to proceed?", or "shall I move on?" — pause only for genuinely missing input, an unresolvable blocker, or list completion.`

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

var _ = Describe("Todo Discipline directive propagation", func() {
	// Every agent that handles multi-step work MUST mandate use of the
	// `todowrite` tool. The bug surfaced in session
	// `089c7cd5-37d8-4a59-868d-366d2dca0cfb` — `default-assistant`
	// (zai/glm-4.6) ran six assistant turns + many tool calls but never
	// created a todo list, despite the user's explicit instruction at index 0.
	// Filesystem-globbing keeps the contract automatic; new prompts inherit
	// the check without spec edits.

	// promptsExempt is the documented allow-list for prompts under
	// internal/prompt/prompts/ that are out of scope of the todo-discipline
	// contract. Each entry MUST cite a reason. When an exemption goes stale,
	// remove the entry and run the spec to confirm propagation.
	var promptsExempt = map[string]string{
		// harness_critic.md is a single-shot, fixed-format reviewer that emits
		// a VERDICT/CONFIDENCE/RUBRIC block for one plan and stops. It has no
		// multi-step "work" — todowrite would be noise. See internal/prompt/
		// prompts/harness_critic.md for the rigid output shape.
		"harness_critic.md": "single-shot fixed-format reviewer; produces one VERDICT/CONFIDENCE block, no multi-step work",
	}

	Describe("internal/prompt/prompts/*.md", func() {
		It("every embedded prompt declares a Todo Discipline section", func() {
			for _, path := range listMarkdownFiles("prompts") {
				if reason, exempt := promptsExempt[filepath.Base(path)]; exempt {
					By("skipping " + path + " — " + reason)
					continue
				}
				content := readFile(path)
				Expect(content).To(ContainSubstring(todoDisciplineHeader),
					"prompt missing '%s' section: %s", todoDisciplineHeader, path)
			}
		})

		It("every embedded prompt carries the canonical todo mandate sentence", func() {
			for _, path := range listMarkdownFiles("prompts") {
				if reason, exempt := promptsExempt[filepath.Base(path)]; exempt {
					By("skipping " + path + " — " + reason)
					continue
				}
				content := readFile(path)
				Expect(content).To(ContainSubstring(canonicalTodoMandate),
					"prompt missing canonical todo mandate sentence: %s", path)
			}
		})

		It("every embedded prompt carries the canonical auto-continue sentence", func() {
			// Bug provenance: user feedback (May 2026) reported agents
			// pausing mid-todo-list to ask "should I continue?" or waiting
			// for an additional prompt before working on the next item.
			// The pre-existing Todo Discipline block (shipped by 3d02901e)
			// said "actively work through the items" but did not explicitly
			// forbid mid-list permission-asking. This assertion pins the
			// extension that closes that gap.
			for _, path := range listMarkdownFiles("prompts") {
				if reason, exempt := promptsExempt[filepath.Base(path)]; exempt {
					By("skipping " + path + " — " + reason)
					continue
				}
				content := readFile(path)
				Expect(content).To(ContainSubstring(canonicalAutoContinueSentence),
					"prompt missing canonical auto-continue sentence: %s", path)
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
