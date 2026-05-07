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
