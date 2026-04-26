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
		It("renders a stable error string with the lifecycle point baked in", func() {
			err := &swarm.GateError{
				GateName: "post-member-plan-reviewer-result-schema",
				GateKind: "builtin:result-schema",
				When:     swarm.LifecyclePostMember,
				SwarmID:  "planning-loop",
				MemberID: "plan-reviewer",
				Reason:   "schema validation failed: missing 'verdict'",
			}

			Expect(err.Error()).To(Equal(
				`gate "post-member-plan-reviewer-result-schema" (builtin:result-schema post-member plan-reviewer) failed for member "plan-reviewer" in swarm "planning-loop": schema validation failed: missing 'verdict'`,
			))
		})

		It("renders a stable error string for swarm-level gates without a member", func() {
			err := &swarm.GateError{
				GateName: "pre-swarm-context-envelope",
				GateKind: "builtin:result-schema",
				When:     swarm.LifecyclePreSwarm,
				SwarmID:  "planning-loop",
				Reason:   "missing required key",
			}

			Expect(err.Error()).To(Equal(
				`gate "pre-swarm-context-envelope" (builtin:result-schema pre) failed for swarm "planning-loop": missing required key`,
			))
		})

		It("unwraps the underlying cause", func() {
			cause := errors.New("decode failure")
			err := &swarm.GateError{Cause: cause}

			Expect(errors.Is(err, cause)).To(BeTrue())
		})
	})

	Describe("MemberGatesFor / SwarmGatesFor", func() {
		mixedGates := func() []swarm.GateSpec {
			return []swarm.GateSpec{
				{Name: "pre-swarm", Kind: "builtin:result-schema", When: swarm.LifecyclePreSwarm},
				{Name: "post-swarm", Kind: "builtin:result-schema", When: swarm.LifecyclePostSwarm},
				{Name: "pre-member-explorer", Kind: "builtin:result-schema", When: swarm.LifecyclePreMember, Target: "explorer"},
				{Name: "post-member-explorer", Kind: "builtin:result-schema", When: swarm.LifecyclePostMember, Target: "explorer"},
				{Name: "post-member-reviewer", Kind: "builtin:result-schema", When: swarm.LifecyclePostMember, Target: "plan-reviewer"},
			}
		}

		It("returns only pre-swarm gates from SwarmGatesFor with when=pre", func() {
			matched := swarm.SwarmGatesFor(mixedGates(), swarm.LifecyclePreSwarm)

			Expect(matched).To(HaveLen(1))
			Expect(matched[0].Name).To(Equal("pre-swarm"))
		})

		It("returns only post-swarm gates from SwarmGatesFor with when=post", func() {
			matched := swarm.SwarmGatesFor(mixedGates(), swarm.LifecyclePostSwarm)

			Expect(matched).To(HaveLen(1))
			Expect(matched[0].Name).To(Equal("post-swarm"))
		})

		It("returns the pre-member match for the targeted member from MemberGatesFor", func() {
			matched := swarm.MemberGatesFor(mixedGates(), swarm.LifecyclePreMember, "explorer")

			Expect(matched).To(HaveLen(1))
			Expect(matched[0].Name).To(Equal("pre-member-explorer"))
		})

		It("returns post-member matches scoped to the targeted member from MemberGatesFor", func() {
			matched := swarm.MemberGatesFor(mixedGates(), swarm.LifecyclePostMember, "plan-reviewer")

			Expect(matched).To(HaveLen(1))
			Expect(matched[0].Name).To(Equal("post-member-reviewer"))
		})

		It("returns an empty slice when SwarmGatesFor is called with a member-level when value", func() {
			matched := swarm.SwarmGatesFor(mixedGates(), swarm.LifecyclePostMember)

			Expect(matched).NotTo(BeNil())
			Expect(matched).To(BeEmpty())
		})

		It("returns an empty slice when MemberGatesFor is called with a swarm-level when value", func() {
			matched := swarm.MemberGatesFor(mixedGates(), swarm.LifecyclePreSwarm, "explorer")

			Expect(matched).NotTo(BeNil())
			Expect(matched).To(BeEmpty())
		})
	})

	Describe("Manifest gate validation rejects malformed lifecycle pairings", func() {
		baseManifest := func() *swarm.Manifest {
			return &swarm.Manifest{
				SchemaVersion: swarm.SchemaVersionV1,
				ID:            "planning-loop",
				Lead:          "planner",
				Members:       []string{"plan-reviewer"},
			}
		}

		It("rejects pre-swarm gates that carry a non-empty target", func() {
			m := baseManifest()
			m.Harness.Gates = []swarm.GateSpec{
				{Name: "pre-with-target", Kind: "builtin:result-schema", When: swarm.LifecyclePreSwarm, Target: "explorer"},
			}

			err := m.Validate(nil)

			Expect(err).To(HaveOccurred())
			var ve *swarm.ValidationError
			Expect(errors.As(err, &ve)).To(BeTrue())
			Expect(ve.Field).To(Equal("harness.gates[0].target"))
			Expect(ve.Message).To(ContainSubstring("must not specify a target"))
		})

		It("rejects post-swarm gates that carry a non-empty target", func() {
			m := baseManifest()
			m.Harness.Gates = []swarm.GateSpec{
				{Name: "post-with-target", Kind: "builtin:result-schema", When: swarm.LifecyclePostSwarm, Target: "explorer"},
			}

			err := m.Validate(nil)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("must not specify a target"))
		})

		It("rejects pre-member gates that omit the target", func() {
			m := baseManifest()
			m.Harness.Gates = []swarm.GateSpec{
				{Name: "pre-member-empty-target", Kind: "builtin:result-schema", When: swarm.LifecyclePreMember},
			}

			err := m.Validate(nil)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("requires a target"))
		})

		It("rejects gates with an unknown when value", func() {
			m := baseManifest()
			m.Harness.Gates = []swarm.GateSpec{
				{Name: "weird-when", Kind: "builtin:result-schema", When: "midstream"},
			}

			err := m.Validate(nil)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unknown lifecycle point"))
		})

		It("accepts pre-swarm gates with no target", func() {
			m := baseManifest()
			m.Harness.Gates = []swarm.GateSpec{
				{Name: "pre", Kind: "builtin:result-schema", When: swarm.LifecyclePreSwarm},
			}

			Expect(m.Validate(nil)).To(Succeed())
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
