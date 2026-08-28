package questionrequest_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/questionrequest"
)

// TestQuestionRequest is the ginkgo suite entrypoint for the
// questionrequest package (consolidated into the canonical
// registry_test.go per the test-file convention ratchet).
func TestQuestionRequest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "QuestionRequest Suite")
}

// The question request registry mirrors the permissionrequest registry
// contract: Register / Wait / Resolve with duplicate and not-found
// sentinels, per-session pending snapshots, and cancellation cleanup.
var _ = Describe("questionrequest.Registry", func() {
	mkReq := func(id, session string) questionrequest.QuestionRequest {
		return questionrequest.QuestionRequest{
			RequestID:     id,
			ToolName:      "question",
			AgentName:     "coordinator",
			Question:      "Which database?",
			Options:       []string{"postgres", "sqlite"},
			AllowMultiple: false,
			SessionID:     session,
		}
	}

	Describe("Register then Resolve — happy path", func() {
		It("delivers the answer via Wait", func() {
			reg := questionrequest.NewRegistry()
			req := mkReq("q-1", "sess-A")
			Expect(reg.Register(req)).To(Succeed())

			var (
				gotAnswer questionrequest.QuestionAnswer
				gotErr    error
				wg        sync.WaitGroup
			)
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				gotAnswer, gotErr = reg.Wait(ctx, "q-1")
			}()

			Consistently(func() bool {
				return reg.PendingForSession("sess-A") != nil
			}, "100ms", "25ms").Should(BeTrue())

			Expect(reg.Resolve("q-1", questionrequest.QuestionAnswer{
				Answers: []string{"postgres"},
			})).To(Succeed())
			wg.Wait()

			Expect(gotErr).NotTo(HaveOccurred())
			Expect(gotAnswer.RequestID).To(Equal("q-1"))
			Expect(gotAnswer.Answers).To(Equal([]string{"postgres"}))
			Expect(reg.PendingForSession("sess-A")).To(BeEmpty())
			Expect(reg.PendingCount()).To(Equal(0))
		})
	})

	Describe("Resolve before Wait", func() {
		It("delivers the buffered answer to a later Wait", func() {
			reg := questionrequest.NewRegistry()
			Expect(reg.Register(mkReq("q-2", "sess-A"))).To(Succeed())
			Expect(reg.Resolve("q-2", questionrequest.QuestionAnswer{Answers: []string{"sqlite"}})).To(Succeed())

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			answer, err := reg.Wait(ctx, "q-2")
			Expect(err).NotTo(HaveOccurred())
			Expect(answer.Answers).To(Equal([]string{"sqlite"}))
		})
	})

	Describe("Wait on unknown request", func() {
		It("returns ErrQuestionRequestNotFound", func() {
			reg := questionrequest.NewRegistry()
			_, err := reg.Wait(context.Background(), "nope")
			Expect(errors.Is(err, questionrequest.ErrQuestionRequestNotFound)).To(BeTrue())
		})
	})

	Describe("Wait cancellation", func() {
		It("returns the context error and cleans up the pending entry", func() {
			reg := questionrequest.NewRegistry()
			Expect(reg.Register(mkReq("q-3", "sess-B"))).To(Succeed())

			ctx, cancel := context.WithCancel(context.Background())
			var (
				gotErr error
				wg     sync.WaitGroup
			)
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, gotErr = reg.Wait(ctx, "q-3")
			}()
			cancel()
			wg.Wait()

			Expect(gotErr).To(MatchError(context.Canceled))
			Expect(reg.PendingForSession("sess-B")).To(BeEmpty())
			Expect(reg.PendingCount()).To(Equal(0))
		})
	})

	Describe("duplicate Register", func() {
		It("returns ErrQuestionRequestExists", func() {
			reg := questionrequest.NewRegistry()
			Expect(reg.Register(mkReq("q-4", "sess-A"))).To(Succeed())
			err := reg.Register(mkReq("q-4", "sess-A"))
			Expect(errors.Is(err, questionrequest.ErrQuestionRequestExists)).To(BeTrue())
		})
	})

	Describe("Resolve unknown request", func() {
		It("returns ErrQuestionRequestNotFound", func() {
			reg := questionrequest.NewRegistry()
			err := reg.Resolve("nope", questionrequest.QuestionAnswer{})
			Expect(errors.Is(err, questionrequest.ErrQuestionRequestNotFound)).To(BeTrue())
		})
	})

	Describe("Lookup", func() {
		It("returns a value copy of the registered question", func() {
			reg := questionrequest.NewRegistry()
			Expect(reg.Register(mkReq("q-5", "sess-C"))).To(Succeed())

			req, ok := reg.Lookup("q-5")
			Expect(ok).To(BeTrue())
			Expect(req.Question).To(Equal("Which database?"))
			Expect(req.Options).To(Equal([]string{"postgres", "sqlite"}))

			_, ok = reg.Lookup("missing")
			Expect(ok).To(BeFalse())
		})
	})

	Describe("PendingForSession", func() {
		It("tracks per-session question ids in arrival order", func() {
			reg := questionrequest.NewRegistry()
			Expect(reg.Register(mkReq("q-6", "sess-A"))).To(Succeed())
			Expect(reg.Register(mkReq("q-7", "sess-A"))).To(Succeed())
			Expect(reg.Register(mkReq("q-8", "sess-B"))).To(Succeed())

			Expect(reg.PendingForSession("sess-A")).To(Equal([]string{"q-6", "q-7"}))
			Expect(reg.PendingForSession("sess-B")).To(Equal([]string{"q-8"}))

			Expect(reg.Resolve("q-6", questionrequest.QuestionAnswer{Answers: []string{"postgres"}})).To(Succeed())
			Expect(reg.PendingForSession("sess-A")).To(Equal([]string{"q-7"}))
		})
	})
})
