package swarm_test

import (
	"context"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

type scriptedDispatch struct {
	mu       sync.Mutex
	results  []error
	calls    int
	memberID string
	track    *[]string
}

func newScriptedDispatch(memberID string, results ...error) *scriptedDispatch {
	return &scriptedDispatch{memberID: memberID, results: results}
}

func (s *scriptedDispatch) dispatch(_ context.Context, member string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.track != nil {
		*s.track = append(*s.track, member)
	}
	idx := s.calls
	s.calls++
	if idx >= len(s.results) {
		return s.results[len(s.results)-1]
	}
	return s.results[idx]
}

func (s *scriptedDispatch) attempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func retryableErr(msg, member string) error {
	return swarm.NewCategorisedError(swarm.CategoryRetryable, errors.New(msg), member)
}

func terminalErr(msg, member string) error {
	return swarm.NewCategorisedError(swarm.CategoryTerminal, errors.New(msg), member)
}

func defaultRetryPolicy() swarm.RetryPolicy {
	return swarm.RetryPolicy{
		MaxAttempts:    3,
		InitialBackoff: time.Microsecond,
		MaxBackoff:     time.Microsecond,
		Multiplier:     2.0,
		Jitter:         false,
	}
}

func defaultBreaker() swarm.CircuitBreakerConfig {
	return swarm.CircuitBreakerConfig{
		Threshold:        5,
		Cooldown:         50 * time.Millisecond,
		HalfOpenAttempts: 1,
	}
}

var _ = Describe("Runner retry policy", func() {
	It("retries a retryable failure and succeeds on the third attempt", func() {
		dispatch := newScriptedDispatch("explorer",
			retryableErr("net flake 1", "explorer"),
			retryableErr("net flake 2", "explorer"),
			nil,
		)

		runner := swarm.NewRunner(defaultRetryPolicy(), defaultBreaker())

		err := runner.Dispatch(context.Background(), "explorer", dispatch.dispatch)

		Expect(err).NotTo(HaveOccurred())
		Expect(dispatch.attempts()).To(Equal(3))
	})

	It("returns the last retryable error when max_attempts is exhausted", func() {
		dispatch := newScriptedDispatch("explorer",
			retryableErr("flake 1", "explorer"),
			retryableErr("flake 2", "explorer"),
			retryableErr("flake 3", "explorer"),
			retryableErr("flake 4", "explorer"),
		)
		policy := defaultRetryPolicy()
		policy.MaxAttempts = 3

		runner := swarm.NewRunner(policy, defaultBreaker())

		err := runner.Dispatch(context.Background(), "explorer", dispatch.dispatch)

		Expect(err).To(HaveOccurred())
		var ce *swarm.CategorisedError
		Expect(errors.As(err, &ce)).To(BeTrue())
		Expect(ce.Category).To(Equal(swarm.CategoryRetryable))
		Expect(dispatch.attempts()).To(Equal(3))
	})

	It("does not retry a terminal error", func() {
		dispatch := newScriptedDispatch("explorer",
			terminalErr("manifest invalid", "explorer"),
		)

		runner := swarm.NewRunner(defaultRetryPolicy(), defaultBreaker())

		err := runner.Dispatch(context.Background(), "explorer", dispatch.dispatch)

		Expect(err).To(HaveOccurred())
		var ce *swarm.CategorisedError
		Expect(errors.As(err, &ce)).To(BeTrue())
		Expect(ce.Category).To(Equal(swarm.CategoryTerminal))
		Expect(dispatch.attempts()).To(Equal(1))
	})

	It("treats an uncategorised error as terminal", func() {
		dispatch := newScriptedDispatch("explorer", errors.New("plain"))

		runner := swarm.NewRunner(defaultRetryPolicy(), defaultBreaker())

		err := runner.Dispatch(context.Background(), "explorer", dispatch.dispatch)

		Expect(err).To(HaveOccurred())
		Expect(dispatch.attempts()).To(Equal(1))
	})
})

var _ = Describe("Runner circuit breaker", func() {
	makeRetryableFlake := func(threshold int) []error {
		out := make([]error, threshold)
		for i := range out {
			out[i] = retryableErr("flake", "x")
		}
		return out
	}

	It("short-circuits with circuit_open after threshold consecutive retryable failures", func() {
		breaker := defaultBreaker()
		breaker.Threshold = 5
		policy := defaultRetryPolicy()
		policy.MaxAttempts = 1

		runner := swarm.NewRunner(policy, breaker)
		dispatches := make([]*scriptedDispatch, 5)
		for i := 0; i < 5; i++ {
			dispatches[i] = newScriptedDispatch("m", makeRetryableFlake(1)...)
			_ = runner.Dispatch(context.Background(), "m", dispatches[i].dispatch)
		}

		sixth := newScriptedDispatch("m", nil)
		err := runner.Dispatch(context.Background(), "m", sixth.dispatch)

		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, swarm.ErrCircuitOpen)).To(BeTrue())
		Expect(sixth.attempts()).To(Equal(0))
	})

	It("allows a half-open dispatch after the cooldown elapses", func() {
		breaker := defaultBreaker()
		breaker.Threshold = 2
		breaker.Cooldown = 10 * time.Millisecond
		breaker.HalfOpenAttempts = 1

		policy := defaultRetryPolicy()
		policy.MaxAttempts = 1

		runner := swarm.NewRunner(policy, breaker)

		for i := 0; i < 2; i++ {
			d := newScriptedDispatch("m", retryableErr("flake", "m"))
			_ = runner.Dispatch(context.Background(), "m", d.dispatch)
		}

		blocked := newScriptedDispatch("m", nil)
		err := runner.Dispatch(context.Background(), "m", blocked.dispatch)
		Expect(errors.Is(err, swarm.ErrCircuitOpen)).To(BeTrue())

		time.Sleep(15 * time.Millisecond)

		halfOpen := newScriptedDispatch("m", nil)
		err = runner.Dispatch(context.Background(), "m", halfOpen.dispatch)

		Expect(err).NotTo(HaveOccurred())
		Expect(halfOpen.attempts()).To(Equal(1))
	})

	It("does not trip the breaker on terminal errors", func() {
		breaker := defaultBreaker()
		breaker.Threshold = 2

		policy := defaultRetryPolicy()
		policy.MaxAttempts = 1

		runner := swarm.NewRunner(policy, breaker)

		for i := 0; i < 5; i++ {
			d := newScriptedDispatch("m", terminalErr("nope", "m"))
			_ = runner.Dispatch(context.Background(), "m", d.dispatch)
		}

		next := newScriptedDispatch("m", nil)
		err := runner.Dispatch(context.Background(), "m", next.dispatch)

		Expect(err).NotTo(HaveOccurred())
		Expect(next.attempts()).To(Equal(1))
	})

	It("resets the failure count on a successful dispatch", func() {
		breaker := defaultBreaker()
		breaker.Threshold = 3

		policy := defaultRetryPolicy()
		policy.MaxAttempts = 1

		runner := swarm.NewRunner(policy, breaker)

		_ = runner.Dispatch(context.Background(), "m", newScriptedDispatch("m", retryableErr("f", "m")).dispatch)
		_ = runner.Dispatch(context.Background(), "m", newScriptedDispatch("m", retryableErr("f", "m")).dispatch)

		err := runner.Dispatch(context.Background(), "m", newScriptedDispatch("m", nil).dispatch)
		Expect(err).NotTo(HaveOccurred())

		_ = runner.Dispatch(context.Background(), "m", newScriptedDispatch("m", retryableErr("f", "m")).dispatch)
		_ = runner.Dispatch(context.Background(), "m", newScriptedDispatch("m", retryableErr("f", "m")).dispatch)

		next := newScriptedDispatch("m", nil)
		err = runner.Dispatch(context.Background(), "m", next.dispatch)
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("Runner sub_swarm_path tracing", func() {
	It("attaches the configured path to retryable errors", func() {
		dispatch := newScriptedDispatch("explorer",
			retryableErr("net flake", "explorer"),
		)

		policy := defaultRetryPolicy()
		policy.MaxAttempts = 1

		runner := swarm.NewRunner(policy, defaultBreaker()).WithSubSwarmPath("bug-hunt/cluster-2")

		err := runner.Dispatch(context.Background(), "explorer", dispatch.dispatch)

		Expect(err).To(HaveOccurred())
		var ce *swarm.CategorisedError
		Expect(errors.As(err, &ce)).To(BeTrue())
		Expect(ce.SubSwarmPath).To(Equal("bug-hunt/cluster-2"))
		Expect(err.Error()).To(ContainSubstring("bug-hunt/cluster-2/explorer"))
	})

	It("attaches the path to terminal errors as well", func() {
		dispatch := newScriptedDispatch("planner", terminalErr("bad manifest", "planner"))

		runner := swarm.NewRunner(defaultRetryPolicy(), defaultBreaker()).WithSubSwarmPath("root/qa")

		err := runner.Dispatch(context.Background(), "planner", dispatch.dispatch)

		var ce *swarm.CategorisedError
		Expect(errors.As(err, &ce)).To(BeTrue())
		Expect(ce.SubSwarmPath).To(Equal("root/qa"))
	})
})
