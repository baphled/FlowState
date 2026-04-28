package swarm_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/swarm"
)

func evidenceGate() swarm.GateSpec {
	return swarm.GateSpec{
		Name:      "post-code-reviewer-evidence-grounding",
		Kind:      swarm.EvidenceGroundingGateKind,
		When:      swarm.LifecyclePostMember,
		Target:    "Code-Reviewer",
		OutputKey: "code-review-findings",
	}
}

func evidenceArgs(store coordination.Store) swarm.GateArgs {
	return swarm.GateArgs{
		SwarmID:     "bug-hunt",
		ChainPrefix: "bug-hunt",
		MemberID:    "Code-Reviewer",
		CoordStore:  store,
	}
}

func writeBugFindings(store coordination.Store, payload []byte) {
	Expect(store.Set("bug-hunt/Code-Reviewer/code-review-findings", payload)).To(Succeed())
}

func writeRepoFile(repoRoot, relPath, body string) {
	full := filepath.Join(repoRoot, relPath)
	Expect(os.MkdirAll(filepath.Dir(full), 0o755)).To(Succeed())
	Expect(os.WriteFile(full, []byte(body), 0o644)).To(Succeed())
}

var _ = Describe("evidence-grounding gate", func() {
	var (
		repoRoot string
		store    coordination.Store
		runner   swarm.GateRunner
	)

	BeforeEach(func() {
		repoRoot = GinkgoT().TempDir()
		store = coordination.NewMemoryStore()
		runner = swarm.NewEvidenceGroundingRunner(repoRoot)
	})

	It("passes when every finding's evidence appears in its cited file", func() {
		writeRepoFile(repoRoot, "internal/recall/chain.go",
			"if err == nil { entry.vector = vector }\n")
		writeBugFindings(store, []byte(`{
			"findings": [
				{
					"severity": "major",
					"description": "embedding error swallowed",
					"file": "internal/recall/chain.go",
					"line": 92,
					"evidence": "if err == nil { entry.vector = vector }"
				}
			]
		}`))

		err := runner.Run(context.Background(), evidenceGate(), evidenceArgs(store))

		Expect(err).NotTo(HaveOccurred())
	})

	It("fails when a finding's evidence is not present in the cited file", func() {
		writeRepoFile(repoRoot, "internal/recall/chain.go",
			"completely different file content\n")
		writeBugFindings(store, []byte(`{
			"findings": [
				{
					"severity": "critical",
					"description": "hallucinated finding",
					"file": "internal/recall/chain.go",
					"line": 92,
					"evidence": "if err == nil { entry.vector = vector }"
				}
			]
		}`))

		err := runner.Run(context.Background(), evidenceGate(), evidenceArgs(store))

		var gateErr *swarm.GateError
		Expect(errors.As(err, &gateErr)).To(BeTrue())
		Expect(gateErr.Reason).To(ContainSubstring("1 ungrounded finding"))
		Expect(gateErr.Reason).To(ContainSubstring("internal/recall/chain.go:92"))
		Expect(gateErr.Reason).To(ContainSubstring("evidence snippet does not appear"))
	})

	It("aggregates every ungrounded finding into a single error", func() {
		writeRepoFile(repoRoot, "a.go", "real content of a\n")
		writeRepoFile(repoRoot, "b.go", "real content of b\n")
		writeBugFindings(store, []byte(`{
			"findings": [
				{"severity": "major", "description": "first", "file": "a.go", "line": 1, "evidence": "fabricated A"},
				{"severity": "major", "description": "second", "file": "b.go", "line": 2, "evidence": "fabricated B"},
				{"severity": "minor", "description": "third", "file": "a.go", "line": 3, "evidence": "real content of a"}
			]
		}`))

		err := runner.Run(context.Background(), evidenceGate(), evidenceArgs(store))

		var gateErr *swarm.GateError
		Expect(errors.As(err, &gateErr)).To(BeTrue())
		Expect(gateErr.Reason).To(ContainSubstring("2 ungrounded finding"))
		Expect(gateErr.Reason).To(ContainSubstring("finding[0]"))
		Expect(gateErr.Reason).To(ContainSubstring("finding[1]"))
		Expect(gateErr.Reason).NotTo(ContainSubstring("finding[2]"),
			"the third finding's evidence is real and must not appear in the failure list")
	})

	It("fails when the cited file does not exist", func() {
		writeBugFindings(store, []byte(`{
			"findings": [
				{
					"severity": "major",
					"description": "wrong file path",
					"file": "internal/does-not-exist.go",
					"line": 1,
					"evidence": "anything"
				}
			]
		}`))

		err := runner.Run(context.Background(), evidenceGate(), evidenceArgs(store))

		var gateErr *swarm.GateError
		Expect(errors.As(err, &gateErr)).To(BeTrue())
		Expect(gateErr.Reason).To(ContainSubstring("file unreadable"))
	})

	It("skips findings without file or evidence", func() {
		writeBugFindings(store, []byte(`{
			"findings": [
				{"severity": "minor", "description": "general nit, no specific location"},
				{"severity": "nit", "description": "package-level observation", "file": ""}
			]
		}`))

		err := runner.Run(context.Background(), evidenceGate(), evidenceArgs(store))

		Expect(err).NotTo(HaveOccurred(),
			"shape enforcement (every finding must have evidence) belongs to the result-schema gate that runs before this one")
	})

	It("returns a typed gate failure when the coord-store is nil", func() {
		err := runner.Run(context.Background(), evidenceGate(), swarm.GateArgs{
			SwarmID:     "bug-hunt",
			ChainPrefix: "bug-hunt",
			MemberID:    "Code-Reviewer",
		})

		var gateErr *swarm.GateError
		Expect(errors.As(err, &gateErr)).To(BeTrue())
		Expect(gateErr.Reason).To(Equal("coordination store unavailable"))
	})

	It("returns a typed gate failure when the payload is not JSON", func() {
		writeBugFindings(store, []byte("not-json"))

		err := runner.Run(context.Background(), evidenceGate(), evidenceArgs(store))

		var gateErr *swarm.GateError
		Expect(errors.As(err, &gateErr)).To(BeTrue())
		Expect(gateErr.Reason).To(ContainSubstring("decoding bug-findings payload"))
	})

	It("memoises file reads when several findings cite the same file", func() {
		const filePath = "shared.go"
		writeRepoFile(repoRoot, filePath, "func A() {}\nfunc B() {}\nfunc C() {}\n")
		writeBugFindings(store, []byte(`{
			"findings": [
				{"severity": "major", "description": "1", "file": "shared.go", "line": 1, "evidence": "func A()"},
				{"severity": "major", "description": "2", "file": "shared.go", "line": 2, "evidence": "func B()"},
				{"severity": "major", "description": "3", "file": "shared.go", "line": 3, "evidence": "func C()"}
			]
		}`))

		err := runner.Run(context.Background(), evidenceGate(), evidenceArgs(store))

		Expect(err).NotTo(HaveOccurred(),
			"three findings citing the same file all ground successfully on a single underlying read")
	})
})
