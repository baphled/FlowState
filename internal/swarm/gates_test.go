package swarm_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/swarm"
)

func newGateStore(seed map[string][]byte) coordination.Store {
	store := coordination.NewMemoryStore()
	for k, v := range seed {
		Expect(store.Set(k, v)).To(Succeed())
	}
	return store
}

func planningLoopGate() swarm.GateSpec {
	return swarm.GateSpec{
		Name:      "post-member-plan-reviewer-result-schema",
		Kind:      "builtin:result-schema",
		SchemaRef: swarm.ReviewVerdictV1Name,
		When:      swarm.LifecyclePostMember,
		Target:    "plan-reviewer",
	}
}

func planningLoopArgs(store coordination.Store) swarm.GateArgs {
	return swarm.GateArgs{
		SwarmID:     "planning-loop",
		ChainPrefix: "planning",
		MemberID:    "plan-reviewer",
		CoordStore:  store,
	}
}

type stubRunner struct {
	calls []swarm.GateSpec
	err   error
}

func (s *stubRunner) Run(_ context.Context, gate swarm.GateSpec, _ swarm.GateArgs) error {
	s.calls = append(s.calls, gate)
	return s.err
}

var _ = Describe("swarm gates (T-swarm-3 Phase 1)", func() {
	BeforeEach(func() {
		swarm.ClearSchemasForTest()
		Expect(swarm.SeedDefaultSchemas()).To(Succeed())
	})

	Describe("PostMemberGatesFor", func() {
		It("returns only post-member gates targeting the member", func() {
			gates := []swarm.GateSpec{
				{Name: "pre", Kind: "builtin:result-schema", When: "pre"},
				{Name: "match", Kind: "builtin:result-schema", When: swarm.LifecyclePostMember, Target: "plan-reviewer"},
				{Name: "other", Kind: "builtin:result-schema", When: swarm.LifecyclePostMember, Target: "explorer"},
				{Name: "match-2", Kind: "builtin:result-schema", When: swarm.LifecyclePostMember, Target: "plan-reviewer"},
			}

			matched := swarm.PostMemberGatesFor(gates, "plan-reviewer")

			Expect(matched).To(HaveLen(2))
			Expect(matched[0].Name).To(Equal("match"))
			Expect(matched[1].Name).To(Equal("match-2"))
		})

		It("returns an empty slice when no gates match", func() {
			matched := swarm.PostMemberGatesFor(nil, "plan-reviewer")

			Expect(matched).NotTo(BeNil())
			Expect(matched).To(BeEmpty())
		})
	})

	Describe("MultiRunner", func() {
		It("dispatches to the runner registered under the gate kind", func() {
			runner := &stubRunner{}
			multi := swarm.NewMultiRunner()
			multi.Register("builtin:result-schema", runner)

			err := multi.Run(context.Background(), planningLoopGate(), planningLoopArgs(coordination.NewMemoryStore()))

			Expect(err).NotTo(HaveOccurred())
			Expect(runner.calls).To(HaveLen(1))
			Expect(runner.calls[0].Name).To(Equal("post-member-plan-reviewer-result-schema"))
		})

		It("returns a typed GateError when no runner is registered for the kind", func() {
			multi := swarm.NewMultiRunner()

			err := multi.Run(context.Background(), planningLoopGate(), planningLoopArgs(coordination.NewMemoryStore()))

			var gateErr *swarm.GateError
			Expect(errors.As(err, &gateErr)).To(BeTrue())
			Expect(gateErr.Reason).To(ContainSubstring(`no runner registered for kind "builtin:result-schema"`))
			Expect(gateErr.GateName).To(Equal("post-member-plan-reviewer-result-schema"))
			Expect(gateErr.MemberID).To(Equal("plan-reviewer"))
		})

		It("propagates the underlying runner's error verbatim", func() {
			cause := errors.New("backend exploded")
			runner := &stubRunner{err: cause}
			multi := swarm.NewMultiRunner()
			multi.Register("builtin:result-schema", runner)

			err := multi.Run(context.Background(), planningLoopGate(), planningLoopArgs(coordination.NewMemoryStore()))

			Expect(err).To(MatchError(cause))
		})
	})

	Describe("RegisterSchema / LookupSchema", func() {
		It("rejects an empty name", func() {
			err := swarm.RegisterSchema("", swarm.ReviewVerdictV1Schema())

			Expect(err).To(MatchError(ContainSubstring("name must be non-empty")))
		})

		It("rejects a nil schema", func() {
			err := swarm.RegisterSchema("any", nil)

			Expect(err).To(MatchError(ContainSubstring("schema must be non-nil")))
		})

		It("seeds review-verdict-v1 from SeedDefaultSchemas", func() {
			resolved, ok := swarm.LookupSchema(swarm.ReviewVerdictV1Name)

			Expect(ok).To(BeTrue())
			Expect(resolved).NotTo(BeNil())
		})
	})

	Describe("builtin:result-schema runner", func() {
		var (
			runner swarm.GateRunner
			gate   swarm.GateSpec
		)

		BeforeEach(func() {
			runner = swarm.NewResultSchemaRunner()
			gate = planningLoopGate()
		})

		It("passes when the verdict matches the schema", func() {
			store := newGateStore(map[string][]byte{
				"planning/plan-reviewer/review": []byte(`{"verdict":"approve","reasoning":"looks good"}`),
			})

			err := runner.Run(context.Background(), gate, planningLoopArgs(store))

			Expect(err).NotTo(HaveOccurred())
		})

		It("falls back to the generic output key when the reviewer-specific key is absent", func() {
			gate.Target = "explorer"
			store := newGateStore(map[string][]byte{
				"planning/explorer/output": []byte(`{"verdict":"approve"}`),
			})
			args := planningLoopArgs(store)
			args.MemberID = "explorer"

			err := runner.Run(context.Background(), gate, args)

			Expect(err).NotTo(HaveOccurred())
		})

		It("returns a GateError when the verdict is missing the required field", func() {
			store := newGateStore(map[string][]byte{
				"planning/plan-reviewer/review": []byte(`{"reasoning":"forgot the verdict"}`),
			})

			err := runner.Run(context.Background(), gate, planningLoopArgs(store))

			var gateErr *swarm.GateError
			Expect(errors.As(err, &gateErr)).To(BeTrue())
			Expect(gateErr.Reason).To(ContainSubstring("schema validation failed"))
			Expect(gateErr.GateName).To(Equal(gate.Name))
			Expect(gateErr.MemberID).To(Equal("plan-reviewer"))
		})

		It("returns a GateError when the schema_ref is empty", func() {
			gate.SchemaRef = ""
			store := newGateStore(map[string][]byte{
				"planning/plan-reviewer/review": []byte(`{"verdict":"approve"}`),
			})

			err := runner.Run(context.Background(), gate, planningLoopArgs(store))

			var gateErr *swarm.GateError
			Expect(errors.As(err, &gateErr)).To(BeTrue())
			Expect(gateErr.Reason).To(ContainSubstring("missing schema_ref"))
		})

		It("returns a GateError when the schema_ref is unknown to the registry", func() {
			gate.SchemaRef = "ghost-schema"
			store := newGateStore(map[string][]byte{
				"planning/plan-reviewer/review": []byte(`{"verdict":"approve"}`),
			})

			err := runner.Run(context.Background(), gate, planningLoopArgs(store))

			var gateErr *swarm.GateError
			Expect(errors.As(err, &gateErr)).To(BeTrue())
			Expect(gateErr.Reason).To(ContainSubstring(`schema_ref "ghost-schema" is not registered`))
		})

		It("returns a GateError when the coord-store key is absent", func() {
			err := runner.Run(context.Background(), gate, planningLoopArgs(coordination.NewMemoryStore()))

			var gateErr *swarm.GateError
			Expect(errors.As(err, &gateErr)).To(BeTrue())
			Expect(gateErr.Reason).To(ContainSubstring("no member output found"))
		})

		It("returns a GateError when the coord-store payload is not valid JSON", func() {
			store := newGateStore(map[string][]byte{
				"planning/plan-reviewer/review": []byte(`{not json`),
			})

			err := runner.Run(context.Background(), gate, planningLoopArgs(store))

			var gateErr *swarm.GateError
			Expect(errors.As(err, &gateErr)).To(BeTrue())
			Expect(gateErr.Reason).To(ContainSubstring("decoding member output as JSON"))
		})

		It("returns a GateError when the coordination store is nil", func() {
			args := planningLoopArgs(nil)

			err := runner.Run(context.Background(), gate, args)

			var gateErr *swarm.GateError
			Expect(errors.As(err, &gateErr)).To(BeTrue())
			Expect(gateErr.Reason).To(ContainSubstring("coordination store unavailable"))
		})
	})

	Describe("GateError", func() {
		It("renders a stable error string", func() {
			err := &swarm.GateError{
				GateName: "post-member-plan-reviewer-result-schema",
				GateKind: "builtin:result-schema",
				SwarmID:  "planning-loop",
				MemberID: "plan-reviewer",
				Reason:   "schema validation failed: missing 'verdict'",
			}

			Expect(err.Error()).To(Equal(
				`gate "post-member-plan-reviewer-result-schema" (builtin:result-schema) failed for member "plan-reviewer" in swarm "planning-loop": schema validation failed: missing 'verdict'`,
			))
		})

		It("unwraps the underlying cause", func() {
			cause := errors.New("decode failure")
			err := &swarm.GateError{Cause: cause}

			Expect(errors.Is(err, cause)).To(BeTrue())
		})
	})

	Describe("NewContext.Gates", func() {
		It("propagates harness gates from the manifest into the runtime envelope", func() {
			manifest := &swarm.Manifest{
				ID:   "planning-loop",
				Lead: "planner",
				Harness: swarm.HarnessConfig{
					Gates: []swarm.GateSpec{planningLoopGate()},
				},
			}

			ctx := swarm.NewContext("planning-loop", manifest)

			Expect(ctx.Gates).To(HaveLen(1))
			Expect(ctx.Gates[0].Name).To(Equal("post-member-plan-reviewer-result-schema"))
		})
	})
})
