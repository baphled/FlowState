package question_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/questionrequest"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/question"
)

// Question tool tests cover metadata reporting (name, description,
// schema, Timeout), the blocking execution path that registers a
// pending question and returns the operator's answer, and the timeout
// disposition that yields a clear no-answer result.
var _ = Describe("Question tool", func() {
	Describe("metadata", func() {
		var toolUnderTest *question.Tool

		BeforeEach(func() {
			toolUnderTest = question.New(questionrequest.NewRegistry(), 0)
		})

		It("reports its name as 'question'", func() {
			Expect(toolUnderTest.Name()).To(Equal("question"))
		})

		It("provides a non-empty description", func() {
			Expect(toolUnderTest.Description()).NotTo(BeEmpty())
		})

		It("declares an object-typed schema", func() {
			Expect(toolUnderTest.Schema().Type).To(Equal("object"))
		})

		It("overrides the engine timeout with the configured window", func() {
			Expect(toolUnderTest.Timeout()).To(Equal(question.DefaultTimeout))
		})
	})

	Describe("Execute", func() {
		It("blocks then returns the operator's answer", func() {
			registry := questionrequest.NewRegistry()
			ctx := context.WithValue(context.Background(), session.IDKey{}, "sess-1")
			input := tool.Input{
				Name: "question",
				Arguments: map[string]any{
					"question":       "Which database should I use?",
					"options":        []any{"postgres", "sqlite"},
					"allow_multiple": false,
				},
			}

			type outcome struct {
				result tool.Result
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := question.New(registry, time.Minute).Execute(ctx, input)
				done <- outcome{result: result, err: err}
			}()

			Eventually(func() []string {
				return registry.PendingForSession("sess-1")
			}, "2s", "10ms").Should(HaveLen(1))

			requestID := registry.PendingForSession("sess-1")[0]
			req, ok := registry.Lookup(requestID)
			Expect(ok).To(BeTrue())
			Expect(req.Question).To(Equal("Which database should I use?"))
			Expect(req.Options).To(ConsistOf("postgres", "sqlite"))

			Expect(registry.Resolve(requestID, questionrequest.QuestionAnswer{Answers: []string{"postgres"}})).To(Succeed())

			select {
			case out := <-done:
				Expect(out.err).NotTo(HaveOccurred())
				Expect(out.result.Output).To(ContainSubstring("postgres"))
				Expect(out.result.Metadata).To(HaveKeyWithValue("answers", []string{"postgres"}))
			case <-time.After(2 * time.Second):
				Fail("question tool did not return after answer")
			}
		})

		It("returns a no-answer result when the timeout elapses", func() {
			registry := questionrequest.NewRegistry()
			result, err := question.New(registry, 20*time.Millisecond).Execute(context.Background(), tool.Input{
				Name:      "question",
				Arguments: map[string]any{"question": "Anybody there?"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("No answer received"))
			Expect(result.Metadata).To(HaveKeyWithValue("timed_out", true))
			Expect(registry.PendingCount()).To(Equal(0))
		})

		It("rejects a missing question argument without registering", func() {
			registry := questionrequest.NewRegistry()
			_, err := question.New(registry, time.Minute).Execute(context.Background(), tool.Input{
				Name:      "question",
				Arguments: map[string]any{},
			})
			Expect(err).To(MatchError("question argument is required"))
			Expect(registry.PendingCount()).To(Equal(0))
		})

		It("publishes lifecycle events on the bus", func() {
			registry := questionrequest.NewRegistry()
			bus := eventbus.NewEventBus()
			required := make(chan string, 1)
			bus.Subscribe("question.required", func(_ any) {
				required <- "fired"
			})

			go func() {
				_, _ = question.NewWithBus(registry, bus, time.Minute).Execute(context.Background(), tool.Input{
					Name:      "question",
					Arguments: map[string]any{"question": "Bus wired?"},
				})
			}()

			Eventually(required, "2s", "10ms").Should(Receive())
		})
	})
})
