package swarm_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

var _ = Describe("Recover", func() {
	It("resumes a session whose lease fence is still current", func() {
		r := swarm.Recover{}
		cp := swarm.Checkpoint{SessionID: "sess-1", RemainingBudget: 50, LeaseFence: 7}

		result, resumeErr := r.Resume("sess-1", cp, 7)

		Expect(result.Resumed).To(BeTrue())
		Expect(resumeErr).NotTo(HaveOccurred())
	})

	It("refuses a checkpoint taken for another session", func() {
		r := swarm.Recover{}
		cp := swarm.Checkpoint{SessionID: "sess-other", RemainingBudget: 50, LeaseFence: 7}

		result, _ := r.Resume("sess-1", cp, 7)

		Expect(result.Reason).To(ContainSubstring("another session"))
	})

	It("refuses a session whose lease fence moved on", func() {
		r := swarm.Recover{}
		cp := swarm.Checkpoint{SessionID: "sess-1", RemainingBudget: 50, LeaseFence: 7}

		result, _ := r.Resume("sess-1", cp, 9)

		Expect(result.Reason).To(ContainSubstring("fence moved on"))
	})

	It("fails closed on an empty session id", func() {
		r := swarm.Recover{}
		cp := swarm.Checkpoint{SessionID: "sess-1", RemainingBudget: 50, LeaseFence: 7}

		_, err := r.Resume("", cp, 7)

		Expect(err).To(MatchError(ContainSubstring("session id must not be empty")))
	})
})
