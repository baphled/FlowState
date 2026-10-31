package validation_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/validation"
)

func hexify(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = 'a'
	}
	return string(out)
}

func validReceipt() validation.Receipt {
	return validation.Receipt{
		SchemaVersion:  "m1-acceptance-receipt-v2",
		CapabilityID:   "M1-VALIDATION",
		RepoState:      validation.RepoState{CommitSHA: hexify(40), Dirty: false, ManifestSHA256: hexify(64), JobID: "job-1"},
		AuthorIdentity: "author@example.test",
		Evidence: []validation.EvidenceItem{{
			Command: "make check", ExpectedExitCode: 0, ExitCode: 0,
			Stdout:        validation.ArtefactRef{Path: "stdout.txt", SHA256: hexify(64)},
			Stderr:        validation.ArtefactRef{Path: "stderr.txt", SHA256: hexify(64)},
			CapturedAtUTC: "2026-10-30T00:00:00Z",
		}},
		GateOutcome:   "pass",
		ObservedModel: validation.ObservedModel{Provider: "zai", ModelID: "glm-4.6", Harness: "flowstate", Source: "observed"},
		Review:        validation.Review{ReviewerIdentity: "reviewer@example.test", EvidenceRef: "evidence-1", CandidateSHA: hexify(40), Verdict: "accept"},
		ValidatorRun:  validation.ValidatorRun{ExitCode: 0, DocsPackManifestSHA256: hexify(64), Artefact: validation.ArtefactRef{Path: "report.json", SHA256: hexify(64)}},
		MetadataRef:   "dropbox:/FlowState/Metadata/attempts/2026-10-30-m1-validation/receipt.json",
		ArtefactRoot:  "dropbox:/FlowState/Metadata/attempts/2026-10-30-m1-validation/artefacts",
	}
}

func validPacket() validation.WorkPacket {
	return validation.WorkPacket{
		SchemaVersion: "work-packet-v1", Sources: []string{"internal/validation"},
		BaseSHA: hexify(40), Tools: []string{"go"}, Scope: []string{"internal/validation"},
		BudgetTokens: 1000, Admitted: false,
	}
}

func validArtefact() validation.ArtefactVerification {
	return validation.ArtefactVerification{Path: "report.json", ClaimedSHA256: hexify(64), ActualSHA256: hexify(64), Present: true}
}

func validReviewIdentity() validation.ReviewIdentity {
	return validation.ReviewIdentity{
		ReviewerIdentity: "reviewer@example.test", AuthorIdentity: "author@example.test",
		CandidateSHA: hexify(40), EvidenceReviewed: true, ReviewerResolves: true, Verdict: "accept",
	}
}

func validCandidate() validation.Candidate {
	manifest := []byte(`{"name":"m1"}`)
	return validation.Candidate{
		CommitSHA: hexify(40), Dirty: false, ManifestSHA256: validation.ManifestDigest(manifest),
		ManifestBytes: manifest, ChangedPaths: []string{"internal/validation/receipt.go"}, CapabilityID: "M1-VALIDATION",
	}
}

var _ = Describe("ValidateReceipt", func() {
	It("accepts a fully valid receipt with zero violations", func() {
		Expect(validation.ValidateReceipt(validReceipt())).To(BeEmpty())
	})

	It("rejects a wrong schema_version", func() {
		r := validReceipt()
		r.SchemaVersion = "m1-acceptance-receipt-v1"
		Expect(validation.ValidateReceipt(r)).To(ContainElement(ContainSubstring("schema_version")))
	})

	It("rejects an invented capability id", func() {
		r := validReceipt()
		r.CapabilityID = "M1-FUTURE"
		Expect(validation.ValidateReceipt(r)).To(ContainElement(ContainSubstring("capability_id")))
	})

	It("rejects a dirty repo state", func() {
		r := validReceipt()
		r.RepoState.Dirty = true
		Expect(validation.ValidateReceipt(r)).To(ContainElement(ContainSubstring("dirty")))
	})

	It("rejects a malformed commit sha", func() {
		r := validReceipt()
		r.RepoState.CommitSHA = "deadbeef"
		Expect(validation.ValidateReceipt(r)).To(ContainElement(ContainSubstring("commit_sha")))
	})

	It("rejects empty evidence", func() {
		r := validReceipt()
		r.Evidence = nil
		Expect(validation.ValidateReceipt(r)).To(ContainElement(ContainSubstring("evidence")))
	})

	It("rejects evidence without a stdout path", func() {
		r := validReceipt()
		r.Evidence[0].Stdout = validation.ArtefactRef{SHA256: hexify(64)}
		Expect(validation.ValidateReceipt(r)).To(ContainElement(ContainSubstring("stdout")))
	})

	It("rejects a non-zero validator exit code", func() {
		r := validReceipt()
		r.ValidatorRun.ExitCode = 1
		Expect(validation.ValidateReceipt(r)).To(ContainElement(ContainSubstring("validator_run.exit_code")))
	})

	It("rejects a metadata ref outside the dropbox attempts path", func() {
		r := validReceipt()
		r.MetadataRef = "file:///tmp/receipt.json"
		Expect(validation.ValidateReceipt(r)).To(ContainElement(ContainSubstring("metadata_ref")))
	})
})

var _ = Describe("CheckReceiptIndex", func() {
	It("accepts an index that agrees with the receipt file", func() {
		r := validReceipt()
		Expect(validation.CheckReceiptIndex([]validation.Receipt{r}, map[string]string{r.CapabilityID: r.MetadataRef})).To(BeEmpty())
	})

	It("rejects an index entry that disagrees with the receipt file", func() {
		r := validReceipt()
		index := map[string]string{r.CapabilityID: "dropbox:/FlowState/Metadata/attempts/other/receipt.json"}
		Expect(validation.CheckReceiptIndex([]validation.Receipt{r}, index)).To(ContainElement(ContainSubstring("disagrees")))
	})

	It("rejects a receipt file missing from the index", func() {
		r := validReceipt()
		Expect(validation.CheckReceiptIndex([]validation.Receipt{r}, map[string]string{})).To(ContainElement(ContainSubstring("missing from index")))
	})

	It("rejects an index entry with no receipt file", func() {
		r := validReceipt()
		Expect(validation.CheckReceiptIndex(nil, map[string]string{r.CapabilityID: r.MetadataRef})).To(ContainElement(ContainSubstring("missing receipt file")))
	})
})

var _ = Describe("ValidateWorkPacket", func() {
	It("accepts a valid proposed packet", func() {
		Expect(validation.ValidateWorkPacket(validPacket())).To(BeEmpty())
	})

	It("rejects an already-admitted packet", func() {
		p := validPacket()
		p.Admitted = true
		Expect(validation.ValidateWorkPacket(p)).To(ContainElement(ContainSubstring("admitted")))
	})

	It("rejects a packet without sources", func() {
		p := validPacket()
		p.Sources = nil
		Expect(validation.ValidateWorkPacket(p)).To(ContainElement(ContainSubstring("sources")))
	})
})

var _ = Describe("ValidateCandidatePaths", func() {
	It("accepts paths inside the closed allowlist", func() {
		Expect(validation.ValidateCandidatePaths([]string{"internal/validation/receipt.go"})).To(BeEmpty())
	})

	It("rejects a path outside the closed allowlist", func() {
		Expect(validation.ValidateCandidatePaths([]string{"evil/payload.sh"})).To(ContainElement(ContainSubstring("allowlist")))
	})
})

var _ = Describe("ValidateCandidateCapability", func() {
	It("accepts a known capability with the T019 prerequisite passed", func() {
		Expect(validation.ValidateCandidateCapability("M1-VALIDATION", validation.T019Pass)).To(BeEmpty())
	})

	It("rejects an invented capability", func() {
		Expect(validation.ValidateCandidateCapability("M2-MAGIC", validation.T019Pass)).To(ContainElement(ContainSubstring("invented capability")))
	})

	It("rejects implementation before the T019 prerequisite", func() {
		Expect(validation.ValidateCandidateCapability("M1-VALIDATION", "T019-failed")).To(ContainElement(ContainSubstring("T019")))
	})
})

var _ = Describe("ValidateTests", func() {
	It("accepts evidence where actual exit equals expected exit", func() {
		e := validation.EvidenceItem{Command: "go test ./...", ExpectedExitCode: 0, ExitCode: 0, Stdout: validation.ArtefactRef{Path: "o", SHA256: hexify(64)}, Stderr: validation.ArtefactRef{Path: "e", SHA256: hexify(64)}}
		Expect(validation.ValidateTests([]validation.EvidenceItem{e}, []string{"TestA"}, nil)).To(BeEmpty())
	})

	It("rejects an actual exit differing from the expected exit", func() {
		e := validation.EvidenceItem{Command: "go test ./...", ExpectedExitCode: 0, ExitCode: 2}
		Expect(validation.ValidateTests([]validation.EvidenceItem{e}, []string{"TestA"}, nil)).To(ContainElement(ContainSubstring("exit")))
	})

	It("rejects an empty test selection", func() {
		e := validation.EvidenceItem{Command: "go test ./...", ExpectedExitCode: 0, ExitCode: 0}
		Expect(validation.ValidateTests([]validation.EvidenceItem{e}, nil, nil)).To(ContainElement(ContainSubstring("selection must not be empty")))
	})

	It("rejects an undefined test name", func() {
		e := validation.EvidenceItem{Command: "go test ./...", ExpectedExitCode: 0, ExitCode: 0}
		Expect(validation.ValidateTests([]validation.EvidenceItem{e}, []string{""}, nil)).To(ContainElement(ContainSubstring("undefined test")))
	})

	It("rejects a skipped test", func() {
		e := validation.EvidenceItem{Command: "go test ./...", ExpectedExitCode: 0, ExitCode: 0}
		Expect(validation.ValidateTests([]validation.EvidenceItem{e}, []string{"TestA"}, map[string]bool{"TestA": true})).To(ContainElement(ContainSubstring("skipped test")))
	})
})

var _ = Describe("ValidateReview", func() {
	It("accepts a distinct resolvable reviewer who reviewed the evidence", func() {
		Expect(validation.ValidateReview(validReviewIdentity())).To(BeEmpty())
	})

	It("rejects a missing reviewer identity", func() {
		id := validReviewIdentity()
		id.ReviewerIdentity = ""
		Expect(validation.ValidateReview(id)).To(ContainElement(ContainSubstring("identity missing")))
	})

	It("rejects an unverifiable reviewer identity", func() {
		id := validReviewIdentity()
		id.ReviewerResolves = false
		Expect(validation.ValidateReview(id)).To(ContainElement(ContainSubstring("does not resolve")))
	})

	It("rejects a self-review", func() {
		id := validReviewIdentity()
		id.ReviewerIdentity = id.AuthorIdentity
		Expect(validation.ValidateReview(id)).To(ContainElement(ContainSubstring("self-review")))
	})

	It("rejects unreviewed evidence", func() {
		id := validReviewIdentity()
		id.EvidenceReviewed = false
		Expect(validation.ValidateReview(id)).To(ContainElement(ContainSubstring("not reviewed")))
	})
})

var _ = Describe("ValidateManifestFreshness", func() {
	It("accepts a manifest digest equal to the current bytes digest", func() {
		Expect(validation.ValidateManifestFreshness(validation.ManifestDigest([]byte(`{"a":1}`)), []byte(`{"a":1}`))).To(BeEmpty())
	})

	It("rejects a stale manifest digest (GF-R03)", func() {
		Expect(validation.ValidateManifestFreshness(hexify(64), []byte(`{"a":1}`))).To(ContainElement(ContainSubstring("stale manifest")))
	})
})

var _ = Describe("ValidateArtefacts", func() {
	It("accepts present artefacts whose digests match", func() {
		a := validation.ArtefactVerification{Path: "stdout.txt", ClaimedSHA256: hexify(64), ActualSHA256: hexify(64), Present: true}
		Expect(validation.ValidateArtefacts([]validation.ArtefactVerification{a})).To(BeEmpty())
	})

	It("rejects a missing artefact", func() {
		a := validation.ArtefactVerification{Path: "stdout.txt", Present: false}
		Expect(validation.ValidateArtefacts([]validation.ArtefactVerification{a})).To(ContainElement(ContainSubstring("missing artefact")))
	})

	It("rejects a tampered artefact digest", func() {
		a := validation.ArtefactVerification{Path: "stdout.txt", ClaimedSHA256: hexify(64), ActualSHA256: hexify(64)[:63] + "b", Present: true}
		Expect(validation.ValidateArtefacts([]validation.ArtefactVerification{a})).To(ContainElement(ContainSubstring("tampered")))
	})
})

var _ = Describe("ValidateAcceptance", func() {
	indexFor := func(r validation.Receipt) map[string]string { return map[string]string{r.CapabilityID: r.MetadataRef} }

	It("accepts a fully valid acceptance set", func() {
		r := validReceipt()
		Expect(validation.ValidateAcceptance(r, validCandidate(), validReviewIdentity(), []validation.ArtefactVerification{validArtefact()}, indexFor(r))).To(BeEmpty())
	})

	It("rejects a wrong candidate sha", func() {
		r := validReceipt()
		c := validCandidate()
		c.CommitSHA = "b" + hexify(39)
		Expect(validation.ValidateAcceptance(r, c, validReviewIdentity(), []validation.ArtefactVerification{validArtefact()}, indexFor(r))).To(ContainElement(ContainSubstring("candidate sha differs")))
	})

	It("rejects a dirty candidate tree", func() {
		r := validReceipt()
		c := validCandidate()
		c.Dirty = true
		Expect(validation.ValidateAcceptance(r, c, validReviewIdentity(), []validation.ArtefactVerification{validArtefact()}, indexFor(r))).To(ContainElement(ContainSubstring("dirty")))
	})

	It("rejects a rejected review verdict", func() {
		r := validReceipt()
		r.Review.Verdict = "reject"
		Expect(validation.ValidateAcceptance(r, validCandidate(), validReviewIdentity(), []validation.ArtefactVerification{validArtefact()}, indexFor(r))).To(ContainElement(ContainSubstring("not accepted")))
	})

	It("rejects a stale manifest digest (GF-R03)", func() {
		r := validReceipt()
		c := validCandidate()
		c.ManifestSHA256 = hexify(64)
		Expect(validation.ValidateAcceptance(r, c, validReviewIdentity(), []validation.ArtefactVerification{validArtefact()}, indexFor(r))).To(ContainElement(ContainSubstring("stale manifest")))
	})
})
