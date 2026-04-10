package execution_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/execution"
	"github.com/baphled/flowstate/internal/harness"
	"github.com/baphled/flowstate/internal/provider"
)

// fakeStreamer satisfies harness.Streamer for testing.
type fakeStreamer struct {
	responses []string
	callCount int
	errOnCall int
	err       error
}

func (f *fakeStreamer) Stream(_ context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
	if f.err != nil && f.callCount == f.errOnCall {
		f.callCount++
		return nil, f.err
	}
	resp := ""
	if f.callCount < len(f.responses) {
		resp = f.responses[f.callCount]
	}
	f.callCount++
	ch := make(chan provider.StreamChunk, 2)
	ch <- provider.StreamChunk{Content: resp}
	ch <- provider.StreamChunk{Done: true}
	close(ch)
	return ch, nil
}

// fakeValidator satisfies harness.Validator for testing.
type fakeValidator struct {
	valid bool
	score float64
}

func (v *fakeValidator) Validate(_ string) (*harness.ValidationResult, error) {
	return &harness.ValidationResult{Valid: v.valid, Score: v.score}, nil
}

// fakeObserver records the last outcome delivered to it.
type fakeObserver struct {
	called  bool
	outcome execution.Outcome
}

func (o *fakeObserver) OnOutcome(outcome execution.Outcome) {
	o.called = true
	o.outcome = outcome
}

var _ = Describe("Loop", func() {
	Describe("NewLoop", func() {
		It("returns a non-nil loop with default configuration", func() {
			loop := execution.NewLoop()
			Expect(loop).NotTo(BeNil())
		})
	})

	Describe("Evaluate", func() {
		Context("when no validator is set", func() {
			It("passes on the first attempt with a perfect score", func() {
				streamer := &fakeStreamer{responses: []string{"hello"}}
				loop := execution.NewLoop()

				result, err := loop.Evaluate(context.Background(), streamer, "agent1", "msg")

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
				Expect(result.Output).To(Equal("hello"))
				Expect(result.FinalScore).To(Equal(1.0))
				Expect(result.AttemptCount).To(Equal(1))
			})
		})

		Context("when the validator passes immediately", func() {
			It("returns a passing result on the first attempt", func() {
				streamer := &fakeStreamer{responses: []string{"good output"}}
				loop := execution.NewLoop(
					execution.WithValidator(&fakeValidator{valid: true, score: 1.0}),
				)

				result, err := loop.Evaluate(context.Background(), streamer, "agent1", "msg")

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(Equal("good output"))
				Expect(result.FinalScore).To(Equal(1.0))
				Expect(result.AttemptCount).To(Equal(1))
			})
		})

		Context("when the validator fails and max retries is 1", func() {
			It("returns a failing result after one attempt", func() {
				streamer := &fakeStreamer{responses: []string{"bad", "still bad"}}
				loop := execution.NewLoop(
					execution.WithValidator(&fakeValidator{valid: false, score: 0.4}),
					execution.WithMaxRetries(1),
				)

				result, err := loop.Evaluate(context.Background(), streamer, "agent1", "msg")

				Expect(err).NotTo(HaveOccurred())
				Expect(result.FinalScore).To(BeNumerically("<", 1.0))
				Expect(result.AttemptCount).To(Equal(1))
			})
		})

		Context("when the validator fails then passes on retry", func() {
			It("returns a passing result after two attempts", func() {
				streamer := &fakeStreamer{responses: []string{"bad", "good"}}
				callCount := 0
				loop := execution.NewLoop(
					execution.WithValidator(&fakeValidator{}),
					execution.WithRetryStrategy(execution.DefaultRetryStrategy{MaxRetries: 3}),
				)
				// Use a custom validator that fails first, passes second
				failThenPass := &toggleValidator{failUntil: 1, callCount: &callCount}
				loop2 := execution.NewLoop(
					execution.WithValidator(failThenPass),
					execution.WithRetryStrategy(execution.DefaultRetryStrategy{MaxRetries: 3}),
				)

				result, err := loop2.Evaluate(context.Background(), streamer, "agent1", "msg")

				Expect(err).NotTo(HaveOccurred())
				Expect(result.FinalScore).To(Equal(1.0))
				Expect(result.AttemptCount).To(Equal(2))
				_ = loop
			})
		})

		Context("when the streamer returns an error on the first call", func() {
			It("propagates the error", func() {
				streamer := &fakeStreamer{err: errors.New("stream error"), errOnCall: 0}
				loop := execution.NewLoop()

				result, err := loop.Evaluate(context.Background(), streamer, "agent1", "msg")

				Expect(err).To(HaveOccurred())
				Expect(result).To(BeNil())
			})
		})

		Context("when a context is cancelled before execution", func() {
			It("returns a cancelled result without error", func() {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()

				streamer := &fakeStreamer{responses: []string{"output"}}
				loop := execution.NewLoop()

				result, err := loop.Evaluate(ctx, streamer, "agent1", "msg")

				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
			})
		})

		Context("when an OutcomeObserver is registered", func() {
			It("calls the observer once with the final outcome", func() {
				streamer := &fakeStreamer{responses: []string{"output"}}
				obs := &fakeObserver{}
				loop := execution.NewLoop(execution.WithOutcomeObserver(obs))

				_, err := loop.Evaluate(context.Background(), streamer, "agent1", "msg")

				Expect(err).NotTo(HaveOccurred())
				Expect(obs.called).To(BeTrue())
				Expect(obs.outcome.Attempts).To(Equal(1))
				Expect(obs.outcome.StopReason).To(Equal(execution.StopReasonPassed))
			})
		})
	})

	Describe("StreamEvaluate", func() {
		It("returns a channel with content and a done chunk", func() {
			streamer := &fakeStreamer{responses: []string{"streamed output"}}
			loop := execution.NewLoop()

			ch, err := loop.StreamEvaluate(context.Background(), streamer, "agent1", "msg")

			Expect(err).NotTo(HaveOccurred())
			Expect(ch).NotTo(BeNil())

			var chunks []provider.StreamChunk
			for chunk := range ch {
				chunks = append(chunks, chunk)
			}
			Expect(chunks).To(HaveLen(2))
			Expect(chunks[0].Content).To(Equal("streamed output"))
			Expect(chunks[1].Done).To(BeTrue())
		})

		It("always closes the channel even when the streamer errors", func() {
			streamer := &fakeStreamer{err: errors.New("fail"), errOnCall: 0}
			loop := execution.NewLoop()

			ch, err := loop.StreamEvaluate(context.Background(), streamer, "agent1", "msg")

			Expect(err).NotTo(HaveOccurred())
			Eventually(ch).Should(BeClosed())
		})
	})

	Describe("DefaultRetryStrategy", func() {
		It("does not retry when attempt equals MaxRetries", func() {
			s := execution.DefaultRetryStrategy{MaxRetries: 2}
			result := &harness.EvaluationResult{FinalScore: 0.5}
			Expect(s.ShouldRetry(2, result)).To(BeFalse())
		})

		It("retries when attempt is below MaxRetries and score is below 1.0", func() {
			s := execution.DefaultRetryStrategy{MaxRetries: 3}
			result := &harness.EvaluationResult{FinalScore: 0.5}
			Expect(s.ShouldRetry(1, result)).To(BeTrue())
		})

		It("does not retry when score is already 1.0", func() {
			s := execution.DefaultRetryStrategy{MaxRetries: 3}
			result := &harness.EvaluationResult{FinalScore: 1.0}
			Expect(s.ShouldRetry(1, result)).To(BeFalse())
		})
	})
})

// toggleValidator fails for the first failUntil calls then passes.
type toggleValidator struct {
	failUntil int
	callCount *int
}

func (v *toggleValidator) Validate(_ string) (*harness.ValidationResult, error) {
	*v.callCount++
	if *v.callCount <= v.failUntil {
		return &harness.ValidationResult{Valid: false, Score: 0.0}, nil
	}
	return &harness.ValidationResult{Valid: true, Score: 1.0}, nil
}
