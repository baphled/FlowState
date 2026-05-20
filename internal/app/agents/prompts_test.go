// Package agents_test pins structural contracts on the markdown agent prompts
// shipped in this directory. These prompts are loaded by the agent platform at
// runtime; structural drift (a missing anchor directive, a missing Turn Rules
// section) silently regresses agent behaviour on long tool-result waves. The
// contract enforced here pairs with the engine-side context anchor reminder
// added in `c5595a77 fix(engine,prompt): anchor agent on user prompt after
// tool-result waves` — see also internal/prompt/prompts/default-assistant.md
// (canonical wording) and internal/engine/engine.go::appendToolResultsBatchToMessages.
package agents_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// canonicalAnchorSentence is the case-sensitive substring every agent prompt
// MUST carry. The clause is intentionally identical across prompts so the
// runtime guard is consistent regardless of which agent answers a turn.
const canonicalAnchorSentence = "Anchor every response on the user's most recent user-role message"

// turnRulesHeader is the section marker that flags the agent has the response-
// shape guard alongside the anchor clause. Both halves are required.
const turnRulesHeader = "Turn Rules"

// todoDisciplineHeader is the section marker that flags the agent carries the
// universal todo-list discipline. Sibling to "Turn Rules" — both are always-on
// behavioural guards independent of the tool loop.
const todoDisciplineHeader = "Todo Discipline"

// canonicalTodoMandate is the case-sensitive substring every agent prompt MUST
// carry on its Todo Discipline section. The clause names the actual tool
// (`todowrite`, registered at internal/tool/todo/todo.go) so agents have no
// ambiguity about which tool to call. Pairs with session
// `089c7cd5-37d8-4a59-868d-366d2dca0cfb` where default-assistant ran six
// assistant turns without ever creating a todo list despite an explicit user
// instruction at index 0.
const canonicalTodoMandate = "Always use the `todowrite` tool to track multi-step work; do not start work on a multi-step task without first recording it."

// canonicalAutoContinueSentence is the case-sensitive substring every agent
// prompt MUST carry alongside the canonical todo mandate. User feedback (May
// 2026): agents were pausing mid-todo-list to ask "should I continue?" or
// waiting for an additional prompt before working on the next item. Direct
// quote: "If the agent has tasks to do, unless it needs my input, it should
// just continue with the work, and not ask for me to continue, or add another
// prompt." The clause pins three legitimate pause reasons and three
// anti-pattern phrasings so every agent has the same explicit guard.
const canonicalAutoContinueSentence = `Once the list is recorded, work through it without asking the user "should I continue?", "do you want me to proceed?", or "shall I move on?" — pause only for genuinely missing input, an unresolvable blocker, or list completion.`

// canonicalTodoUpdateSentence is the case-sensitive substring every agent
// prompt MUST carry on its Progress bullet. Bug provenance: session
// 59b4e1a2-daf9-44f2-b179-fa0757c34f02 emitted 4 todowrite calls vs ~94 bash
// calls because todowrite was the only API and replaced the entire list.
// Models batched many flips into one call against the prompt's "never batch"
// instruction. The fix introduced the patch-op sibling tool todo_update; this
// pin names the tool explicitly so per-transition discipline survives prompt
// drift. Registered at internal/tool/todo/update.go.
const canonicalTodoUpdateSentence = "Use `todo_update` for every status transition — one call per flip, marking each item `in_progress` when you start it and `completed` when it is done. Reserve `todowrite` for the initial list creation only"

// exemptedPrompts is the documented allow-list for prompts that are out of
// scope of the propagation contract. Each entry MUST cite a reason and a
// long-lived owner.
//
// Owner: this allow-list belongs to the next agent that touches Turn Rules
// across the prompt fleet. When an exemption goes stale, remove the entry and
// run the spec to confirm propagation.
var exemptedPrompts = map[string]string{
	// planner.md is the active scope of the `Agent Prompt Upgrade` plan
	// (vault: 1. Projects/FlowState/Plans/Agent Prompt Upgrade.md). The
	// 2026-05 propagation slice (Turn Rules anchor) explicitly leaves it
	// untouched to avoid stomping in-flight plan changes. Remove this
	// exemption once that plan ships its rewrite.
	"planner.md": "owned by Agent Prompt Upgrade plan; do not modify until that plan ships",
}

func TestAgents(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Agents Suite")
}

var _ = Describe("Turn Rules anchor directive on agent prompts", func() {
	// Filesystem-globbing keeps the contract automatic: any new
	// internal/app/agents/*.md file is checked without spec edits.

	It("every agent prompt declares a Turn Rules section", func() {
		for _, path := range listAgentPrompts() {
			if reason, exempt := exemptedPrompts[filepath.Base(path)]; exempt {
				By("skipping " + path + " — " + reason)
				continue
			}
			content := readFile(path)
			Expect(content).To(ContainSubstring(turnRulesHeader),
				"agent prompt missing '%s' section: %s", turnRulesHeader, path)
		}
	})

	It("every agent prompt carries the canonical anchor sentence", func() {
		for _, path := range listAgentPrompts() {
			if reason, exempt := exemptedPrompts[filepath.Base(path)]; exempt {
				By("skipping " + path + " — " + reason)
				continue
			}
			content := readFile(path)
			Expect(content).To(ContainSubstring(canonicalAnchorSentence),
				"agent prompt missing canonical anchor sentence: %s", path)
		}
	})
})

// todoExempt is the documented allow-list for agent prompts excluded from the
// Todo Discipline contract. Each entry MUST cite a reason and (where
// applicable) the trigger that retires the exemption. The list is kept
// deliberately separate from `exemptedPrompts` (Turn Rules) — the two
// disciplines have different exemption rationales and may diverge over time.
var todoExempt = map[string]string{
	// planner.md is the active scope of the `Agent Prompt Upgrade` plan
	// (vault: 1. Projects/FlowState/Plans/Agent Prompt Upgrade.md). The
	// 2026-05 propagation slice (Todo Discipline) explicitly leaves it
	// untouched to avoid stomping in-flight plan changes — the plan owns
	// the canonical "single-task discipline" + "completion signal"
	// rewrites and includes its own todo-flavoured discipline. Remove
	// this exemption once that plan ships its rewrite.
	"planner.md": "owned by Agent Prompt Upgrade plan; do not modify until that plan ships",
}

var _ = Describe("Todo Discipline directive on agent prompts", func() {
	// Bug provenance: session `089c7cd5-37d8-4a59-868d-366d2dca0cfb` —
	// `default-assistant` (zai/glm-4.6) ran six assistant turns + many tool
	// calls but never created a todo list, despite the user's explicit
	// instruction at index 0 to use the todo list. The fix mandates every
	// multi-step-capable agent carries the same canonical mandate sentence
	// pinning the actual tool name (`todowrite`).
	//
	// Filesystem-globbing keeps the contract automatic: any new
	// internal/app/agents/*.md file is checked without spec edits.

	It("every agent prompt declares a Todo Discipline section", func() {
		for _, path := range listAgentPrompts() {
			if reason, exempt := todoExempt[filepath.Base(path)]; exempt {
				By("skipping " + path + " — " + reason)
				continue
			}
			content := readFile(path)
			Expect(content).To(ContainSubstring(todoDisciplineHeader),
				"agent prompt missing '%s' section: %s", todoDisciplineHeader, path)
		}
	})

	It("every agent prompt carries the canonical todo mandate sentence", func() {
		for _, path := range listAgentPrompts() {
			if reason, exempt := todoExempt[filepath.Base(path)]; exempt {
				By("skipping " + path + " — " + reason)
				continue
			}
			content := readFile(path)
			Expect(content).To(ContainSubstring(canonicalTodoMandate),
				"agent prompt missing canonical todo mandate sentence: %s", path)
		}
	})

	It("every agent prompt carries the canonical todo_update transition sentence", func() {
		// Bug provenance: session 59b4e1a2-daf9-44f2-b179-fa0757c34f02 —
		// models batched many per-task status flips into one todowrite call
		// because there was no single-task patch API and todowrite replaces
		// the entire list. The fix added todo_update as the patch-op sibling
		// (internal/tool/todo/update.go); this assertion pins the prompt
		// clause that directs every agent to use it for per-transition flips,
		// so the discipline survives future prompt drift.
		for _, path := range listAgentPrompts() {
			if reason, exempt := todoExempt[filepath.Base(path)]; exempt {
				By("skipping " + path + " — " + reason)
				continue
			}
			content := readFile(path)
			Expect(content).To(ContainSubstring(canonicalTodoUpdateSentence),
				"agent prompt missing canonical todo_update transition sentence: %s", path)
		}
	})

	It("every agent prompt carries the canonical auto-continue sentence", func() {
		// Bug provenance: user feedback (May 2026) reported agents pausing
		// mid-todo-list to ask "should I continue?" or waiting for an
		// additional prompt before working on the next item. The pre-existing
		// Todo Discipline block (shipped by 3d02901e) said "actively work
		// through the items" but did not explicitly forbid mid-list
		// permission-asking. This assertion pins the extension that closes
		// that gap — the auto-continue clause names three legitimate pause
		// reasons and three anti-pattern phrasings.
		for _, path := range listAgentPrompts() {
			if reason, exempt := todoExempt[filepath.Base(path)]; exempt {
				By("skipping " + path + " — " + reason)
				continue
			}
			content := readFile(path)
			Expect(content).To(ContainSubstring(canonicalAutoContinueSentence),
				"agent prompt missing canonical auto-continue sentence: %s", path)
		}
	})
})

// PR6/C2 close-out (May 2026): the embed seed contracts. PR4 rewrote the
// per-user copies in `~/.config/flowstate/agents/` to remove the reflex
// "Call skill_load(name) for EACH skill before beginning any work" anchor
// and replace it with a lazy-load instruction. The audit found the
// in-repo siblings (`internal/app/embed.go` `//go:embed agents/*.md`) —
// the source of truth for fresh installs and CI test binaries — still
// shipped the reflex prose. These contracts pin the embed seed so a
// future engineer cannot regress to the reflex anchor on the in-repo
// copies again. The per-user copy is reseeded from the embed FS on first
// install; tightening the in-repo copy is the load-bearing fix.
//
// staleReflexSentence: the literal anchor PR4 retired. Any in-repo
// manifest that re-introduces "for EACH skill before beginning" should
// fail the contract. The wording is deliberately specific to the retired
// version, so the assertion does not over-fire on related future
// language about skill loading.
const staleReflexSentence = "for EACH skill before beginning"

// lazyLoadAnchor: the canonical replacement sentence shipped by PR4. The
// in-repo executor.md MUST carry this exact phrasing so the embed FS
// hands the lazy-load anchor to every fresh install. Sibling rewrites in
// default-assistant.md and planner.md use the same first half but
// diverge on the trailing clause ("plan-execution task" vs
// "multi-step task" vs "multi-step planning task") — the shared prefix
// is the structural pin.
const lazyLoadAnchor = "Your always-active skills are listed in the `<available_skills>` block above. Invoke `skill_load(name)` for a skill only when its domain becomes load-bearing for the current task"

// aTeamScopeGuardSentence: the canonical clause in researcher.md that
// keeps the researcher from re-reading source files when running inside
// the `a-team` swarm and the executor has already produced output. The
// guard was shipped on the per-user copy by an earlier PR4 edit but the
// in-repo seed never received it; PR6/C2 closes that gap.
const aTeamScopeGuardSentence = "When running inside the `a-team` swarm and the executor has already produced a tool/test output earlier in the chain"

// reflexEliminationManifests is the set of in-repo manifests that PR4
// rewrote to remove the stale reflex sentence. Each manifest in this
// list MUST NOT contain the retired anchor, and at least one MUST carry
// the new lazy-load anchor. The map keys the relative filename inside
// the agents package directory.
var reflexEliminationManifests = []string{
	"executor.md",
	"default-assistant.md",
	"planner.md",
}

var _ = Describe("PR6/C2 — embed seed reflex-elimination contract", func() {
	// Bug provenance: post-PR4 audit found that the per-user copies at
	// ~/.config/flowstate/agents/ had the reflex anchor rewritten, but
	// the in-repo siblings embedded into the binary still shipped the
	// retired prose. Fresh installs and CI test binaries would seed the
	// reflex anchor unchanged, silently re-introducing the regression
	// PR4 closed. These specs pin the seed, not the per-user copies.

	for _, name := range reflexEliminationManifests {
		manifestName := name // capture loop var for the closure
		It("does NOT carry the retired reflex sentence: "+manifestName, func() {
			content := readFile(manifestName)
			Expect(content).NotTo(ContainSubstring(staleReflexSentence),
				"in-repo manifest %s still embeds the retired 'EACH skill before beginning' reflex; the embed FS would seed it into fresh installs", manifestName)
		})

		It("carries the canonical lazy-load anchor: "+manifestName, func() {
			content := readFile(manifestName)
			Expect(content).To(ContainSubstring(lazyLoadAnchor),
				"in-repo manifest %s missing the PR4 lazy-load anchor; the per-user copy carries it but the embed FS lags", manifestName)
		})
	}

	It("researcher.md carries the A-Team scope guard sentence", func() {
		// Bug provenance: PR4 added a scope guard to the researcher
		// persona for the a-team swarm — when the executor has already
		// run, the researcher should interpret the executor's output
		// rather than re-read source files unprompted. The per-user
		// copy carries the guard but the in-repo seed did not. PR6/C2
		// closes the asymmetry.
		content := readFile("researcher.md")
		Expect(content).To(ContainSubstring(aTeamScopeGuardSentence),
			"in-repo researcher.md missing the PR4 A-Team scope guard; the embed FS would seed an un-guarded researcher into fresh installs")
	})
})

// listAgentPrompts returns absolute paths to every .md file in the package
// directory. The package is loaded with its own working directory at test
// time, so a relative read of "." is the agent prompt directory.
func listAgentPrompts() []string {
	entries, err := os.ReadDir(".")
	Expect(err).NotTo(HaveOccurred(), "reading agents directory")
	var paths []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		paths = append(paths, e.Name())
	}
	Expect(paths).NotTo(BeEmpty(), "no agent prompts found in package directory")
	return paths
}

func readFile(path string) string {
	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred(), "reading %s", path)
	return string(data)
}
