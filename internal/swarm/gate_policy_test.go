package swarm_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

type recordingRunner struct {
	mu      sync.Mutex
	calls   []string
	respond func(gate swarm.GateSpec) error
}

func (r *recordingRunner) Run(_ context.Context, gate swarm.GateSpec, _ swarm.GateArgs) error {
	r.mu.Lock()
	r.calls = append(r.calls, gate.Name)
	r.mu.Unlock()
	if r.respond != nil {
		return r.respond(gate)
	}
	return nil
}

func (r *recordingRunner) Calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

type contextWatchingRunner struct {
	deadlineSeen bool
	released     chan struct{}
}

func (r *contextWatchingRunner) Run(ctx context.Context, _ swarm.GateSpec, _ swarm.GateArgs) error {
	if _, ok := ctx.Deadline(); ok {
		r.deadlineSeen = true
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.released:
		return nil
	}
}

func emptyArgs() swarm.GateArgs { return swarm.GateArgs{SwarmID: "policy-suite"} }

var _ = Describe("Gate precedence + failurePolicy + timeout (A1/A6)", func() {
	Describe("Precedence enum", func() {
		It("exposes CRITICAL, HIGH, MEDIUM, LOW as constants", func() {
			Expect(string(swarm.PrecedenceCritical)).To(Equal("CRITICAL"))
			Expect(string(swarm.PrecedenceHigh)).To(Equal("HIGH"))
			Expect(string(swarm.PrecedenceMedium)).To(Equal("MEDIUM"))
			Expect(string(swarm.PrecedenceLow)).To(Equal("LOW"))
		})

		It("defaults to MEDIUM when unset", func() {
			gate := swarm.GateSpec{Name: "g", Kind: "builtin:result-schema"}

			Expect(swarm.EffectivePrecedence(gate)).To(Equal(swarm.PrecedenceMedium))
		})

		It("returns the explicit precedence when set", func() {
			gate := swarm.GateSpec{Name: "g", Kind: "builtin:result-schema", Precedence: swarm.PrecedenceCritical}

			Expect(swarm.EffectivePrecedence(gate)).To(Equal(swarm.PrecedenceCritical))
		})
	})

	Describe("FailurePolicy", func() {
		It("defaults to halt when unset", func() {
			gate := swarm.GateSpec{Name: "g", Kind: "builtin:result-schema"}

			Expect(swarm.EffectiveFailurePolicy(gate)).To(Equal(swarm.FailurePolicyHalt))
		})

		It("exposes halt, continue, and warn as constants", func() {
			Expect(string(swarm.FailurePolicyHalt)).To(Equal("halt"))
			Expect(string(swarm.FailurePolicyContinue)).To(Equal("continue"))
			Expect(string(swarm.FailurePolicyWarn)).To(Equal("warn"))
		})
	})

	Describe("Manifest validation accepts the new fields", func() {
		baseManifest := func() *swarm.Manifest {
			return &swarm.Manifest{
				SchemaVersion: swarm.SchemaVersionV1,
				ID:            "policy",
				Lead:          "planner",
				Members:       []string{"reviewer"},
			}
		}

		It("accepts a gate with precedence=HIGH, failurePolicy=warn, timeout=5s", func() {
			m := baseManifest()
			m.Harness.Gates = []swarm.GateSpec{
				{
					Name:          "ok",
					Kind:          "builtin:result-schema",
					When:          swarm.LifecyclePostSwarm,
					Precedence:    swarm.PrecedenceHigh,
					FailurePolicy: swarm.FailurePolicyWarn,
					Timeout:       5 * time.Second,
				},
			}

			Expect(m.Validate(nil)).To(Succeed())
		})

		It("rejects an unknown precedence value", func() {
			m := baseManifest()
			m.Harness.Gates = []swarm.GateSpec{
				{Name: "bad", Kind: "builtin:result-schema", When: "post", Precedence: "URGENT"},
			}

			err := m.Validate(nil)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("precedence"))
		})

		It("rejects an unknown failurePolicy value", func() {
			m := baseManifest()
			m.Harness.Gates = []swarm.GateSpec{
				{Name: "bad", Kind: "builtin:result-schema", When: "post", FailurePolicy: "abort"},
			}

			err := m.Validate(nil)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failurePolicy"))
		})

		It("rejects a negative timeout", func() {
			m := baseManifest()
			m.Harness.Gates = []swarm.GateSpec{
				{Name: "bad", Kind: "builtin:result-schema", When: "post", Timeout: -1 * time.Second},
			}

			err := m.Validate(nil)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("timeout"))
		})
	})

	Describe("YAML round-trip of policy fields", func() {
		It("parses precedence, failurePolicy, and timeout from YAML", func() {
			body := []byte(`schema_version: "1.0.0"
id: policy
lead: planner
members:
  - reviewer
harness:
  gates:
    - name: critical-check
      kind: builtin:result-schema
      when: post
      precedence: CRITICAL
      failurePolicy: warn
      timeout: 5s
`)

			m, err := swarm.UnmarshalManifest(body)

			Expect(err).NotTo(HaveOccurred())
			Expect(m.Harness.Gates).To(HaveLen(1))
			Expect(m.Harness.Gates[0].Precedence).To(Equal(swarm.PrecedenceCritical))
			Expect(m.Harness.Gates[0].FailurePolicy).To(Equal(swarm.FailurePolicyWarn))
			Expect(m.Harness.Gates[0].Timeout).To(Equal(5 * time.Second))
		})
	})

	Describe("SortGatesByPrecedence", func() {
		It("orders CRITICAL before HIGH before MEDIUM before LOW", func() {
			gates := []swarm.GateSpec{
				{Name: "low", Precedence: swarm.PrecedenceLow},
				{Name: "high", Precedence: swarm.PrecedenceHigh},
				{Name: "critical", Precedence: swarm.PrecedenceCritical},
				{Name: "medium", Precedence: swarm.PrecedenceMedium},
			}

			sorted := swarm.SortGatesByPrecedence(gates)

			Expect(sorted).To(HaveLen(4))
			Expect(sorted[0].Name).To(Equal("critical"))
			Expect(sorted[1].Name).To(Equal("high"))
			Expect(sorted[2].Name).To(Equal("medium"))
			Expect(sorted[3].Name).To(Equal("low"))
		})

		It("preserves manifest order for equal precedence (stable sort)", func() {
			gates := []swarm.GateSpec{
				{Name: "first-medium"},
				{Name: "second-medium"},
				{Name: "third-medium"},
			}

			sorted := swarm.SortGatesByPrecedence(gates)

			Expect(sorted).To(HaveLen(3))
			Expect(sorted[0].Name).To(Equal("first-medium"))
			Expect(sorted[1].Name).To(Equal("second-medium"))
			Expect(sorted[2].Name).To(Equal("third-medium"))
		})

		It("treats unset precedence as MEDIUM for ordering", func() {
			gates := []swarm.GateSpec{
				{Name: "unset"},
				{Name: "explicit-medium", Precedence: swarm.PrecedenceMedium},
				{Name: "high", Precedence: swarm.PrecedenceHigh},
			}

			sorted := swarm.SortGatesByPrecedence(gates)

			Expect(sorted[0].Name).To(Equal("high"))
			Expect(sorted[1].Name).To(Equal("unset"))
			Expect(sorted[2].Name).To(Equal("explicit-medium"))
		})

		It("returns a fresh slice (caller mutation does not affect input)", func() {
			gates := []swarm.GateSpec{
				{Name: "low", Precedence: swarm.PrecedenceLow},
				{Name: "high", Precedence: swarm.PrecedenceHigh},
			}

			sorted := swarm.SortGatesByPrecedence(gates)
			sorted[0].Name = "mutated"

			Expect(gates[0].Name).To(Equal("low"))
			Expect(gates[1].Name).To(Equal("high"))
		})
	})

	Describe("MemberGatesFor / SwarmGatesFor honour precedence ordering", func() {
		It("orders post-member matches by precedence, stable on ties", func() {
			gates := []swarm.GateSpec{
				{Name: "a-medium", When: swarm.LifecyclePostMember, Target: "reviewer"},
				{Name: "b-low", When: swarm.LifecyclePostMember, Target: "reviewer", Precedence: swarm.PrecedenceLow},
				{Name: "c-critical", When: swarm.LifecyclePostMember, Target: "reviewer", Precedence: swarm.PrecedenceCritical},
				{Name: "d-medium", When: swarm.LifecyclePostMember, Target: "reviewer"},
			}

			matched := swarm.MemberGatesFor(gates, swarm.LifecyclePostMember, "reviewer")

			Expect(matched).To(HaveLen(4))
			Expect(matched[0].Name).To(Equal("c-critical"))
			Expect(matched[1].Name).To(Equal("a-medium"))
			Expect(matched[2].Name).To(Equal("d-medium"))
			Expect(matched[3].Name).To(Equal("b-low"))
		})

		It("orders pre-swarm matches by precedence", func() {
			gates := []swarm.GateSpec{
				{Name: "low", When: swarm.LifecyclePreSwarm, Precedence: swarm.PrecedenceLow},
				{Name: "high", When: swarm.LifecyclePreSwarm, Precedence: swarm.PrecedenceHigh},
				{Name: "critical", When: swarm.LifecyclePreSwarm, Precedence: swarm.PrecedenceCritical},
			}

			matched := swarm.SwarmGatesFor(gates, swarm.LifecyclePreSwarm)

			Expect(matched).To(HaveLen(3))
			Expect(matched[0].Name).To(Equal("critical"))
			Expect(matched[1].Name).To(Equal("high"))
			Expect(matched[2].Name).To(Equal("low"))
		})
	})

	Describe("Dispatch honours failurePolicy", func() {
		It("halts on the first failure when policy is halt (default)", func() {
			boom := errors.New("schema mismatch")
			runner := &recordingRunner{
				respond: func(gate swarm.GateSpec) error {
					if gate.Name == "first" {
						return boom
					}
					return nil
				},
			}
			gates := []swarm.GateSpec{
				{Name: "first"},
				{Name: "second"},
			}

			report := swarm.Dispatch(context.Background(), runner, gates, emptyArgs())

			Expect(report.Halted).To(BeTrue())
			Expect(report.HaltedBy).To(Equal("first"))
			Expect(report.Err).To(MatchError(boom))
			Expect(runner.Calls()).To(Equal([]string{"first"}))
		})

		It("continues past failure when policy is continue and records it", func() {
			boom := errors.New("schema mismatch")
			runner := &recordingRunner{
				respond: func(gate swarm.GateSpec) error {
					if gate.Name == "first" {
						return boom
					}
					return nil
				},
			}
			gates := []swarm.GateSpec{
				{Name: "first", FailurePolicy: swarm.FailurePolicyContinue},
				{Name: "second"},
			}

			report := swarm.Dispatch(context.Background(), runner, gates, emptyArgs())

			Expect(report.Halted).To(BeFalse())
			Expect(report.Err).To(BeNil())
			Expect(report.Failures).To(HaveLen(1))
			Expect(report.Failures[0].GateName).To(Equal("first"))
			Expect(report.Failures[0].Policy).To(Equal(swarm.FailurePolicyContinue))
			Expect(report.Failures[0].Err).To(MatchError(boom))
			Expect(runner.Calls()).To(Equal([]string{"first", "second"}))
		})

		It("continues past failure when policy is warn and records a warning", func() {
			boom := errors.New("schema mismatch")
			runner := &recordingRunner{
				respond: func(gate swarm.GateSpec) error {
					if gate.Name == "noisy" {
						return boom
					}
					return nil
				},
			}
			gates := []swarm.GateSpec{
				{Name: "noisy", FailurePolicy: swarm.FailurePolicyWarn},
				{Name: "second"},
			}

			report := swarm.Dispatch(context.Background(), runner, gates, emptyArgs())

			Expect(report.Halted).To(BeFalse())
			Expect(report.Err).To(BeNil())
			Expect(report.Warnings).To(HaveLen(1))
			Expect(report.Warnings[0].GateName).To(Equal("noisy"))
			Expect(report.Warnings[0].Err).To(MatchError(boom))
			Expect(runner.Calls()).To(Equal([]string{"noisy", "second"}))
		})

		It("runs gates in precedence order before applying policy", func() {
			runner := &recordingRunner{}
			gates := []swarm.GateSpec{
				{Name: "low", Precedence: swarm.PrecedenceLow},
				{Name: "critical", Precedence: swarm.PrecedenceCritical},
				{Name: "high", Precedence: swarm.PrecedenceHigh},
			}

			report := swarm.Dispatch(context.Background(), runner, gates, emptyArgs())

			Expect(report.Halted).To(BeFalse())
			Expect(runner.Calls()).To(Equal([]string{"critical", "high", "low"}))
		})
	})

	Describe("Dispatch honours per-gate timeout", func() {
		It("treats a gate that exceeds its timeout as a failure per policy=halt", func() {
			runner := &contextWatchingRunner{released: make(chan struct{})}
			gates := []swarm.GateSpec{
				{Name: "slow", Timeout: 25 * time.Millisecond},
			}
			defer close(runner.released)

			start := time.Now()
			report := swarm.Dispatch(context.Background(), runner, gates, emptyArgs())
			elapsed := time.Since(start)

			Expect(report.Halted).To(BeTrue())
			Expect(report.HaltedBy).To(Equal("slow"))
			Expect(report.Err).To(HaveOccurred())
			Expect(elapsed).To(BeNumerically("<", 1*time.Second))
		})

		It("supplies the runner with a deadline on the context when timeout is set", func() {
			runner := &contextWatchingRunner{released: make(chan struct{})}
			close(runner.released)

			report := swarm.Dispatch(context.Background(), runner, []swarm.GateSpec{
				{Name: "with-deadline", Timeout: 50 * time.Millisecond},
			}, emptyArgs())

			Expect(report.Halted).To(BeFalse())
			Expect(runner.deadlineSeen).To(BeTrue())
		})

		It("does not impose a deadline when timeout is zero", func() {
			var sawDeadline atomic.Bool
			runner := &recordingRunner{
				respond: func(_ swarm.GateSpec) error {
					return nil
				},
			}
			gates := []swarm.GateSpec{
				{Name: "no-deadline"},
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			deadlineCheck := &deadlineProbe{seen: &sawDeadline, inner: runner}
			report := swarm.Dispatch(ctx, deadlineCheck, gates, emptyArgs())

			Expect(report.Halted).To(BeFalse())
			Expect(sawDeadline.Load()).To(BeFalse())
		})

		It("treats a timed-out gate with policy=warn as a warning, not a halt", func() {
			runner := &contextWatchingRunner{released: make(chan struct{})}
			gates := []swarm.GateSpec{
				{Name: "warn-on-slow", Timeout: 20 * time.Millisecond, FailurePolicy: swarm.FailurePolicyWarn},
				{Name: "after"},
			}
			defer close(runner.released)
			next := &recordingRunner{}

			multi := &policyDispatchMux{
				routes: map[string]swarm.GateRunner{
					"warn-on-slow": runner,
					"after":        next,
				},
			}

			report := swarm.Dispatch(context.Background(), multi, gates, emptyArgs())

			Expect(report.Halted).To(BeFalse())
			Expect(report.Warnings).To(HaveLen(1))
			Expect(report.Warnings[0].GateName).To(Equal("warn-on-slow"))
			Expect(next.Calls()).To(ContainElement("after"))
		})
	})
})

type deadlineProbe struct {
	seen  *atomic.Bool
	inner swarm.GateRunner
}

func (d *deadlineProbe) Run(ctx context.Context, gate swarm.GateSpec, args swarm.GateArgs) error {
	if _, ok := ctx.Deadline(); ok {
		d.seen.Store(true)
	}
	return d.inner.Run(ctx, gate, args)
}

type policyDispatchMux struct {
	routes map[string]swarm.GateRunner
}

func (m *policyDispatchMux) Run(ctx context.Context, gate swarm.GateSpec, args swarm.GateArgs) error {
	r, ok := m.routes[gate.Name]
	if !ok {
		return nil
	}
	return r.Run(ctx, gate, args)
}
