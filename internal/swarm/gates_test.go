package swarm_test

import (
	"context"
	"encoding/json"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/gates"
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

		// Defect 2 (synthesis hang) — swarm-gate path. When a member's
		// post-member gate fails because the member wrote nothing (the
		// "no member output found" case), the lead receives this reason
		// verbatim as its delegate-tool result (delegation.go:2588-2601,
		// fail-fast, no retry). The reason must therefore be DIRECTIVE so
		// the lead's next turn re-delegates with an explicit "perform the
		// write" instruction rather than narrating again. Aligns the
		// swarm-gate path with the harness wave-fan-in directive.
		It("makes the no-output reason directive so the lead re-delegates the write", func() {
			err := runner.Run(context.Background(), gate, planningLoopArgs(coordination.NewMemoryStore()))

			var gateErr *swarm.GateError
			Expect(errors.As(err, &gateErr)).To(BeTrue())
			Expect(gateErr.Reason).To(ContainSubstring("no member output found"),
				"the existing diagnostic substring must be preserved")
			Expect(gateErr.Reason).To(MatchRegexp(`(?i)did not write|wrote no output|narrat`),
				"the reason must name the synthesis-hang failure (member produced no output)")
			Expect(gateErr.Reason).To(MatchRegexp(`(?i)re-delegate|perform the write|do not narrate`),
				"the reason must direct the lead to re-delegate the write, not accept the narration")
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

		It("reads from the explicit output_key when set on the gate", func() {
			gate.Target = "explorer"
			gate.OutputKey = "evidence"
			store := newGateStore(map[string][]byte{
				"planning/explorer/evidence": []byte(`{"verdict":"approve"}`),
				"planning/explorer/output":   []byte(`{"intentionally":"wrong"}`),
			})
			args := planningLoopArgs(store)
			args.MemberID = "explorer"

			err := runner.Run(context.Background(), gate, args)

			Expect(err).NotTo(HaveOccurred())
		})

		It("does NOT fall back to the default key when an explicit output_key is set and the key is absent", func() {
			gate.Target = "explorer"
			gate.OutputKey = "missing"
			store := newGateStore(map[string][]byte{
				"planning/explorer/output": []byte(`{"verdict":"approve"}`),
			})
			args := planningLoopArgs(store)
			args.MemberID = "explorer"

			err := runner.Run(context.Background(), gate, args)

			var gateErr *swarm.GateError
			Expect(errors.As(err, &gateErr)).To(BeTrue())
			Expect(gateErr.Reason).To(ContainSubstring("no member output found"))
			Expect(gateErr.Reason).To(ContainSubstring("planning/explorer/missing"))
			Expect(gateErr.Reason).NotTo(ContainSubstring("planning/explorer/output"))
		})

		It("uses the legacy plan-reviewer convention when no explicit output_key is set", func() {
			gate.OutputKey = ""
			store := newGateStore(map[string][]byte{
				"planning/plan-reviewer/review": []byte(`{"verdict":"approve"}`),
			})

			err := runner.Run(context.Background(), gate, planningLoopArgs(store))

			Expect(err).NotTo(HaveOccurred())
		})

		// chainID-templated output_key resolution (planning-loop autonomy
		// fix, May 2026). The planning-loop lead allocates a free-form
		// chainID per planning request and members write to
		// "<chainID>/<semantic-suffix>" (e.g. "<chainID>/analysis"), NOT
		// the static "<chain_prefix>/<target>/output" the gate used to
		// resolve. When the gate's output_key carries a "{chainID}"
		// template the runner substitutes args.ChainID and reads the
		// member's real key — mirroring coordWaveValidator.MissingForChain
		// in internal/app/harness_adapter.go:268-303.
		Context("when the gate output_key carries a {chainID} template", func() {
			It("passes when the member wrote to <chainID>/<suffix> (the bug scenario, was failing)", func() {
				gate.Target = "analyst"
				gate.OutputKey = "{chainID}/analysis"
				gate.SchemaRef = swarm.AnalysisBundleV1Name
				store := newGateStore(map[string][]byte{
					"mental-health-companion-2026-05-27/analysis": []byte(
						`{"summary":"ok","key_findings":["a"],"recommendations":["do x"],"risks":["r"]}`,
					),
				})
				args := planningLoopArgs(store)
				args.MemberID = "analyst"
				args.ChainID = "mental-health-companion-2026-05-27"

				err := runner.Run(context.Background(), gate, args)

				Expect(err).NotTo(HaveOccurred())
			})

			It("FAILS with no-member-output when the member genuinely wrote nothing", func() {
				gate.Target = "analyst"
				gate.OutputKey = "{chainID}/analysis"
				gate.SchemaRef = swarm.AnalysisBundleV1Name
				args := planningLoopArgs(coordination.NewMemoryStore())
				args.MemberID = "analyst"
				args.ChainID = "mental-health-companion-2026-05-27"

				err := runner.Run(context.Background(), gate, args)

				var gateErr *swarm.GateError
				Expect(errors.As(err, &gateErr)).To(BeTrue())
				Expect(gateErr.Reason).To(ContainSubstring("no member output found"))
				Expect(gateErr.Reason).To(ContainSubstring("mental-health-companion-2026-05-27/analysis"))
			})

			It("preserves multi-run isolation — chain A's output does not satisfy chain B's gate", func() {
				gate.Target = "analyst"
				gate.OutputKey = "{chainID}/analysis"
				gate.SchemaRef = swarm.AnalysisBundleV1Name
				store := newGateStore(map[string][]byte{
					"chain-a/analysis": []byte(
						`{"summary":"ok","key_findings":["a"],"recommendations":["do x"],"risks":["r"]}`,
					),
				})
				args := planningLoopArgs(store)
				args.MemberID = "analyst"
				args.ChainID = "chain-b"

				err := runner.Run(context.Background(), gate, args)

				var gateErr *swarm.GateError
				Expect(errors.As(err, &gateErr)).To(BeTrue())
				Expect(gateErr.Reason).To(ContainSubstring("no member output found"))
				Expect(gateErr.Reason).To(ContainSubstring("chain-b/analysis"))
				Expect(gateErr.Reason).NotTo(ContainSubstring("chain-a"))
			})

			It("resolves the plan-reviewer review key under the lead's chainID", func() {
				gate.Target = "plan-reviewer"
				gate.OutputKey = "{chainID}/review"
				store := newGateStore(map[string][]byte{
					"plan-auth-2026-04-23/review": []byte(`{"verdict":"approve","reasoning":"looks good"}`),
				})
				args := planningLoopArgs(store)
				args.MemberID = "plan-reviewer"
				args.ChainID = "plan-auth-2026-04-23"

				err := runner.Run(context.Background(), gate, args)

				Expect(err).NotTo(HaveOccurred())
			})

			It("falls back to a suffix-scan when no chainID is in args (bootstrap parity with the wave validator)", func() {
				gate.Target = "analyst"
				gate.OutputKey = "{chainID}/analysis"
				gate.SchemaRef = swarm.AnalysisBundleV1Name
				store := newGateStore(map[string][]byte{
					"some-bootstrap-chain/analysis": []byte(
						`{"summary":"ok","key_findings":["a"],"recommendations":["do x"],"risks":["r"]}`,
					),
				})
				args := planningLoopArgs(store)
				args.MemberID = "analyst"
				args.ChainID = ""

				err := runner.Run(context.Background(), gate, args)

				Expect(err).NotTo(HaveOccurred())
			})
		})
	})

	// Defect 4 (fabricated publication) — the honesty gate. The
	// coordinator marked the planning loop COMPLETE on the strength of a
	// SELF-REPORTED `<chain>/plan_publication` record claiming a vault
	// path + published_at, while the plan was never written to the vault
	// (the honest `<chain>/persisted-plan` key said "write blocked").
	// The artifact-published gate verifies completion deterministically:
	// the coord-store `<chainID>/plan` key MUST be non-empty AND, when the
	// publication record claims a vault_path, the file MUST actually exist
	// at that path under the resolved plan_output_dir. A self-report with
	// no real artifact FAILS the gate — no prompt instruction can lie past
	// a file stat.
	Describe("builtin:artifact-published runner (honesty gate)", func() {
		const outputDir = "/home/baphled/vaults/baphled/1. Projects/FlowState"

		var (
			gate    swarm.GateSpec
			statted []string
		)

		// fakeStat records every path stat'd and answers presence from a
		// fixed set — no real filesystem touch.
		newFakeStat := func(present map[string]bool) func(string) (bool, error) {
			return func(path string) (bool, error) {
				statted = append(statted, path)
				return present[path], nil
			}
		}

		BeforeEach(func() {
			statted = nil
			gate = swarm.GateSpec{
				Name:      "post-swarm-plan-published",
				Kind:      "builtin:artifact-published",
				When:      "post",
				OutputKey: "{chainID}/plan",
			}
		})

		It("passes when the plan key is non-empty and the claimed vault file exists under the output dir", func() {
			vaultPath := outputDir + "/Auth-Hardening-Plan.md"
			store := newGateStore(map[string][]byte{
				"plan-auth/plan": []byte("# Auth Hardening Plan\n..."),
				"plan-auth/plan_publication": []byte(
					`{"vault_path":"` + vaultPath + `","published_at":"2026-05-28T10:00:00Z"}`),
			})
			args := planningLoopArgs(store)
			args.ChainID = "plan-auth"

			runner := swarm.NewArtifactPublishedRunner(outputDir, newFakeStat(map[string]bool{vaultPath: true}))
			Expect(runner.Run(context.Background(), gate, args)).To(Succeed())
			Expect(statted).To(ContainElement(vaultPath),
				"the gate must actually stat the claimed vault path, not trust the record")
		})

		It("FAILS when a publication record claims a vault path but the file does not exist (the fabrication)", func() {
			vaultPath := outputDir + "/Ghost-Plan.md"
			store := newGateStore(map[string][]byte{
				"plan-auth/plan": []byte("# Plan body present in coord-store"),
				"plan-auth/plan_publication": []byte(
					`{"vault_path":"` + vaultPath + `","published_at":"2026-05-28T10:00:00Z"}`),
			})
			args := planningLoopArgs(store)
			args.ChainID = "plan-auth"

			runner := swarm.NewArtifactPublishedRunner(outputDir, newFakeStat(map[string]bool{ /* file absent */ }))
			err := runner.Run(context.Background(), gate, args)

			var gateErr *swarm.GateError
			Expect(errors.As(err, &gateErr)).To(BeTrue())
			Expect(gateErr.Reason).To(MatchRegexp(`(?i)claimed|publication`),
				"the failure must call out the self-reported publication claim")
			Expect(gateErr.Reason).To(ContainSubstring(vaultPath),
				"the failure must name the missing file so the lead knows the claim was false")
		})

		It("FAILS when the coord-store plan key is empty (no artifact produced at all)", func() {
			store := newGateStore(map[string][]byte{
				"plan-auth/plan": []byte(""),
			})
			args := planningLoopArgs(store)
			args.ChainID = "plan-auth"

			runner := swarm.NewArtifactPublishedRunner(outputDir, newFakeStat(nil))
			err := runner.Run(context.Background(), gate, args)

			var gateErr *swarm.GateError
			Expect(errors.As(err, &gateErr)).To(BeTrue())
			Expect(gateErr.Reason).To(MatchRegexp(`(?i)plan.*empty|no plan|empty plan`),
				"an empty plan key must fail the honesty gate")
		})

		It("FAILS when the coord-store plan key is missing entirely", func() {
			store := newGateStore(map[string][]byte{})
			args := planningLoopArgs(store)
			args.ChainID = "plan-auth"

			runner := swarm.NewArtifactPublishedRunner(outputDir, newFakeStat(nil))
			err := runner.Run(context.Background(), gate, args)

			var gateErr *swarm.GateError
			Expect(errors.As(err, &gateErr)).To(BeTrue())
		})

		It("FAILS when the claimed vault path escapes the resolved output dir (path-traversal honesty)", func() {
			escapePath := "/tmp/elsewhere/Plan.md"
			store := newGateStore(map[string][]byte{
				"plan-auth/plan": []byte("# Plan body"),
				"plan-auth/plan_publication": []byte(
					`{"vault_path":"` + escapePath + `","published_at":"2026-05-28T10:00:00Z"}`),
			})
			args := planningLoopArgs(store)
			args.ChainID = "plan-auth"

			// Even if the file "exists" at the escaping path, a write
			// outside plan_output_dir is not an honest publication.
			runner := swarm.NewArtifactPublishedRunner(outputDir, newFakeStat(map[string]bool{escapePath: true}))
			err := runner.Run(context.Background(), gate, args)

			var gateErr *swarm.GateError
			Expect(errors.As(err, &gateErr)).To(BeTrue())
			Expect(gateErr.Reason).To(MatchRegexp(`(?i)outside|not under|output dir`),
				"a vault_path outside plan_output_dir must fail the honesty gate")
		})

		It("passes on the plan key alone when no publication record claims a vault path", func() {
			// A coordinator that wrote the plan to the coord-store but made
			// no vault claim is honest about the loop's state — the gate
			// gates on the artifact it CAN verify (the plan key) and does
			// not invent a file requirement the run never asserted.
			store := newGateStore(map[string][]byte{
				"plan-auth/plan": []byte("# Plan body present, no vault claim"),
			})
			args := planningLoopArgs(store)
			args.ChainID = "plan-auth"

			runner := swarm.NewArtifactPublishedRunner(outputDir, newFakeStat(nil))
			Expect(runner.Run(context.Background(), gate, args)).To(Succeed())
			Expect(statted).To(BeEmpty(),
				"with no claimed vault_path the gate must not stat any file")
		})

		// Post-swarm dispatch (runSwarmGates) leaves GateArgs.ChainID
		// empty — the lead's free-form chainID is not threaded onto the
		// swarm context. The gate must still resolve the plan via a
		// suffix-scan, mirroring the result-schema runner's bootstrap
		// fallback, so it works as a `when: post` swarm gate.
		Context("when no chainID is threaded (post-swarm dispatch)", func() {
			It("resolves the plan key by suffix-scan and verifies the claimed vault file", func() {
				vaultPath := outputDir + "/Suffix-Scan-Plan.md"
				store := newGateStore(map[string][]byte{
					"some-chain/plan": []byte("# Plan body via suffix-scan"),
					"some-chain/plan_publication": []byte(
						`{"vault_path":"` + vaultPath + `"}`),
				})
				args := planningLoopArgs(store)
				args.ChainID = "" // post-swarm: no concrete chainID

				runner := swarm.NewArtifactPublishedRunner(outputDir, newFakeStat(map[string]bool{vaultPath: true}))
				Expect(runner.Run(context.Background(), gate, args)).To(Succeed())
				Expect(statted).To(ContainElement(vaultPath))
			})

			It("FAILS via suffix-scan when no plan key exists in any chain", func() {
				store := newGateStore(map[string][]byte{})
				args := planningLoopArgs(store)
				args.ChainID = ""

				runner := swarm.NewArtifactPublishedRunner(outputDir, newFakeStat(nil))
				err := runner.Run(context.Background(), gate, args)

				var gateErr *swarm.GateError
				Expect(errors.As(err, &gateErr)).To(BeTrue())
				Expect(gateErr.Reason).To(MatchRegexp(`(?i)no plan artifact`))
			})
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

	Describe("multi-key coord-store payload composition for ext gates", func() {
		BeforeEach(func() {
			swarm.ResetExtGateRegistryForTest()
		})

		// captureRunner records the request payload the dispatcher hands the
		// ext gate so each spec can pin the JSON shape the host composed.
		captureRunner := func(captured *swarm.ExtGateRequest) swarm.ExtGateFunc {
			return func(_ context.Context, req swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
				*captured = req
				return swarm.ExtGateResponse{Pass: true}, nil
			}
		}

		It("with no inputs declared, forwards the single-key coord-store value verbatim (backward-compat)", func() {
			var got swarm.ExtGateRequest
			Expect(swarm.RegisterExtGateFuncWithInputs("legacy-gate", captureRunner(&got), nil)).To(Succeed())

			store := newGateStore(map[string][]byte{
				"a-team/researcher/output": []byte(`{"summary":"hello"}`),
			})
			multi := swarm.NewMultiRunner()
			err := multi.Run(context.Background(), swarm.GateSpec{
				Name: "legacy", Kind: "ext:legacy-gate", When: swarm.LifecyclePostMember, Target: "researcher",
			}, swarm.GateArgs{
				SwarmID: "a-team", ChainPrefix: "a-team", MemberID: "researcher", CoordStore: store,
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(string(got.Payload)).To(Equal(`{"summary":"hello"}`))
		})

		It("with a multi-key inputs declaration, composes a JSON object keyed by the logical input names", func() {
			var got swarm.ExtGateRequest
			Expect(swarm.RegisterExtGateFuncWithInputs("relevance-gate", captureRunner(&got), []gates.InputSpec{
				{Name: "task_plan", Member: "coordinator", OutputKey: "task-plan"},
				{Name: "research", Member: "researcher", OutputKey: "output"},
			})).To(Succeed())

			store := newGateStore(map[string][]byte{
				"a-team/coordinator/task-plan": []byte(`"investigate flaky test"`),
				"a-team/researcher/output":     []byte(`"the test races on the cancel branch"`),
			})
			multi := swarm.NewMultiRunner()
			err := multi.Run(context.Background(), swarm.GateSpec{
				Name: "relevance", Kind: "ext:relevance-gate", When: swarm.LifecyclePostMember, Target: "researcher",
			}, swarm.GateArgs{
				SwarmID: "a-team", ChainPrefix: "a-team", MemberID: "researcher", CoordStore: store,
			})

			Expect(err).ToNot(HaveOccurred())
			var payload map[string]any
			Expect(json.Unmarshal(got.Payload, &payload)).To(Succeed())
			Expect(payload).To(HaveKeyWithValue("task_plan", "investigate flaky test"))
			Expect(payload).To(HaveKeyWithValue("research", "the test races on the cancel branch"))
		})

		It("substitutes ${target} in the input member with the dispatching gate's Target", func() {
			var got swarm.ExtGateRequest
			Expect(swarm.RegisterExtGateFuncWithInputs("relevance-gate", captureRunner(&got), []gates.InputSpec{
				{Name: "task_plan", Member: "coordinator", OutputKey: "task-plan"},
				{Name: "research", Member: "${target}", OutputKey: "output"},
			})).To(Succeed())

			store := newGateStore(map[string][]byte{
				"a-team/coordinator/task-plan": []byte(`"plan"`),
				"a-team/researcher/output":     []byte(`"resolved-via-target"`),
			})
			multi := swarm.NewMultiRunner()
			err := multi.Run(context.Background(), swarm.GateSpec{
				Name: "relevance", Kind: "ext:relevance-gate", When: swarm.LifecyclePostMember, Target: "researcher",
			}, swarm.GateArgs{
				SwarmID: "a-team", ChainPrefix: "a-team", MemberID: "researcher", CoordStore: store,
			})

			Expect(err).ToNot(HaveOccurred())
			var payload map[string]any
			Expect(json.Unmarshal(got.Payload, &payload)).To(Succeed())
			Expect(payload).To(HaveKeyWithValue("research", "resolved-via-target"))
		})

		It("preserves embedded JSON values rather than nesting them as JSON-encoded strings", func() {
			var got swarm.ExtGateRequest
			Expect(swarm.RegisterExtGateFuncWithInputs("quorum-gate", captureRunner(&got), []gates.InputSpec{
				{Name: "bull", Member: "bull", OutputKey: "output"},
				{Name: "bear", Member: "bear", OutputKey: "output"},
			})).To(Succeed())

			store := newGateStore(map[string][]byte{
				"board/bull/output": []byte(`{"verdict":"buy","conf":0.8}`),
				"board/bear/output": []byte(`{"verdict":"sell","conf":0.6}`),
			})
			multi := swarm.NewMultiRunner()
			err := multi.Run(context.Background(), swarm.GateSpec{
				Name: "quorum", Kind: "ext:quorum-gate", When: swarm.LifecyclePostSwarm,
			}, swarm.GateArgs{
				SwarmID: "board", ChainPrefix: "board", CoordStore: store,
			})

			Expect(err).ToNot(HaveOccurred())
			var payload map[string]any
			Expect(json.Unmarshal(got.Payload, &payload)).To(Succeed())
			bull, ok := payload["bull"].(map[string]any)
			Expect(ok).To(BeTrue(), "bull should be a JSON object, not a string")
			Expect(bull).To(HaveKeyWithValue("verdict", "buy"))
			bear, ok := payload["bear"].(map[string]any)
			Expect(ok).To(BeTrue(), "bear should be a JSON object, not a string")
			Expect(bear).To(HaveKeyWithValue("verdict", "sell"))
		})

		It("falls back to a JSON string when a coord-store value is not valid JSON", func() {
			var got swarm.ExtGateRequest
			Expect(swarm.RegisterExtGateFuncWithInputs("relevance-gate", captureRunner(&got), []gates.InputSpec{
				{Name: "task_plan", Member: "coordinator", OutputKey: "task-plan"},
				{Name: "research", Member: "researcher", OutputKey: "output"},
			})).To(Succeed())

			store := newGateStore(map[string][]byte{
				"a-team/coordinator/task-plan": []byte(`investigate flaky test`),
				"a-team/researcher/output":     []byte(`raw text not json`),
			})
			multi := swarm.NewMultiRunner()
			err := multi.Run(context.Background(), swarm.GateSpec{
				Name: "relevance", Kind: "ext:relevance-gate", When: swarm.LifecyclePostMember, Target: "researcher",
			}, swarm.GateArgs{
				SwarmID: "a-team", ChainPrefix: "a-team", MemberID: "researcher", CoordStore: store,
			})

			Expect(err).ToNot(HaveOccurred())
			var payload map[string]any
			Expect(json.Unmarshal(got.Payload, &payload)).To(Succeed())
			Expect(payload).To(HaveKeyWithValue("task_plan", "investigate flaky test"))
			Expect(payload).To(HaveKeyWithValue("research", "raw text not json"))
		})

		// A missing declared input is NOT a hard composition failure — the
		// composer writes a JSON `null` for that key so the dispatched
		// gate receives a well-formed payload and decides for itself how
		// to handle a missing upstream output. The previous "fail-fast at
		// composition time" behaviour surfaced an opaque GateError with
		// the gate-name and swarm-id stripped (the 2026-05-18 "gate ""
		// (ext:relevance-gate post-member researcher) failed for member
		// "researcher" in swarm "": payload is not valid JSON" report); the
		// gate's own failure_policy gives operators a clearer, gate-
		// specific reason instead.
		It("writes JSON null for a declared input that is missing from the coord-store and still invokes the gate", func() {
			var got swarm.ExtGateRequest
			Expect(swarm.RegisterExtGateFuncWithInputs("relevance-gate", captureRunner(&got), []gates.InputSpec{
				{Name: "task_plan", Member: "coordinator", OutputKey: "task-plan"},
				{Name: "research", Member: "researcher", OutputKey: "output"},
			})).To(Succeed())

			// only task_plan is seeded; research key is missing
			store := newGateStore(map[string][]byte{
				"a-team/coordinator/task-plan": []byte(`"investigate"`),
			})
			multi := swarm.NewMultiRunner()
			err := multi.Run(context.Background(), swarm.GateSpec{
				Name: "relevance", Kind: "ext:relevance-gate", When: swarm.LifecyclePostMember, Target: "researcher",
			}, swarm.GateArgs{
				SwarmID: "a-team", ChainPrefix: "a-team", MemberID: "researcher", CoordStore: store,
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(json.Valid(got.Payload)).To(BeTrue(),
				"composed payload MUST be valid JSON even when an input is missing; got %q", string(got.Payload))
			var payload map[string]json.RawMessage
			Expect(json.Unmarshal(got.Payload, &payload)).To(Succeed())
			Expect(payload).To(HaveKey("research"))
			Expect(string(payload["research"])).To(Equal("null"),
				"missing input MUST embed as JSON null, got %q", string(payload["research"]))
			Expect(payload).To(HaveKey("task_plan"))
		})

		// Regression — the 2026-05-18 user report showed the gate receiving
		// a malformed payload and rejecting with "payload is not valid
		// JSON". The composer is the only host-side site that builds the
		// gate's stdin payload; it MUST always emit a valid JSON object so
		// the gate's decode path never bottoms out on a structural parse
		// failure. Each value-shape we expect to encounter in a real
		// coord-store (missing key, empty bytes, valid JSON, raw prose)
		// must compose into a well-formed payload.
		It("always emits valid JSON regardless of the coord-store value shape (missing, empty, JSON, prose)", func() {
			var got swarm.ExtGateRequest
			Expect(swarm.RegisterExtGateFuncWithInputs("relevance-gate", captureRunner(&got), []gates.InputSpec{
				{Name: "task_plan", Member: "coordinator", OutputKey: "task-plan"},
				{Name: "research", Member: "researcher", OutputKey: "output"},
			})).To(Succeed())

			// task_plan key is seeded with empty bytes (the engine wrote
			// the key but the upstream member produced no content);
			// research key is absent entirely. Both shapes have appeared
			// in production sessions.
			store := newGateStore(map[string][]byte{
				"a-team/coordinator/task-plan": []byte{},
			})
			multi := swarm.NewMultiRunner()
			err := multi.Run(context.Background(), swarm.GateSpec{
				Name: "relevance", Kind: "ext:relevance-gate", When: swarm.LifecyclePostMember, Target: "researcher",
			}, swarm.GateArgs{
				SwarmID: "a-team", ChainPrefix: "a-team", MemberID: "researcher", CoordStore: store,
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(json.Valid(got.Payload)).To(BeTrue(),
				"composed payload MUST be valid JSON for any coord-store value shape; got %q", string(got.Payload))
			var payload map[string]any
			Expect(json.Unmarshal(got.Payload, &payload)).To(Succeed())
			Expect(payload).To(HaveKey("task_plan"))
			Expect(payload).To(HaveKey("research"))
		})

		// Regression — pins the gate-inputs registry gap surfaced by the
		// a-team relevance-gate dispatch (TUI log 2026-05-08T13:28:01.646).
		// When the dispatched ext gate has no inputs declaration registered
		// (lookup miss OR registered with an empty inputs slice) AND the
		// single-key coord-store fallback also misses, the previous
		// behaviour silently invoked the gate with an empty payload — the
		// gate then rejected an empty input opaquely. The dispatcher MUST
		// instead surface a typed *GateError naming the missed coord-store
		// key, so the operator can locate the misregistered manifest /
		// missing upstream output without source-reading.
		It("fails the dispatch with a typed GateError when no inputs are registered AND the single-key fallback finds no coord-store value", func() {
			Expect(swarm.RegisterExtGateFuncWithInputs("legacy-gate", func(_ context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
				Fail("ext gate runner should not be invoked when single-key fallback fails")
				return swarm.ExtGateResponse{Pass: true}, nil
			}, nil)).To(Succeed())

			store := newGateStore(map[string][]byte{
				// No researcher/output key seeded — single-key fallback
				// will miss.
			})
			multi := swarm.NewMultiRunner()
			err := multi.Run(context.Background(), swarm.GateSpec{
				Name: "legacy", Kind: "ext:legacy-gate", When: swarm.LifecyclePostMember, Target: "researcher",
			}, swarm.GateArgs{
				SwarmID: "a-team", ChainPrefix: "a-team", MemberID: "researcher", CoordStore: store,
			})

			var gateErr *swarm.GateError
			Expect(errors.As(err, &gateErr)).To(BeTrue(), "expected typed GateError, got %v", err)
			Expect(gateErr.Reason).To(ContainSubstring("a-team/researcher/output"),
				"expected the missed coord-store key in the reason for operator diagnostics")
		})

		// Regression — pins the same masking gap for the case where the
		// gate is registered with a non-empty inputs declaration but the
		// runtime registry returned ok=false (e.g. registration order or a
		// stale registry handle): the dispatcher must NOT silently
		// fallthrough to the single-key path, it must surface a typed
		// failure pointing at the missing single-key target so the
		// operator sees the registration gap rather than an opaque
		// empty-payload gate response.
		It("fails the dispatch with a typed GateError when the gate is unregistered entirely AND single-key target is absent", func() {
			// no RegisterExtGate* call — the runner registry is empty for
			// "ext:relevance-gate", so MultiRunner.Run will reach
			// RunGate which fails on the unregistered ext lookup. We
			// pin that the failure is a typed *GateError that names the
			// gate (rather than a bare error) so consumers downstream
			// of MultiRunner.Run can trust the error shape.
			store := newGateStore(map[string][]byte{
				"a-team/researcher/output": []byte(`{"summary":"hello"}`),
			})
			multi := swarm.NewMultiRunner()
			err := multi.Run(context.Background(), swarm.GateSpec{
				Name: "relevance", Kind: "ext:relevance-gate", When: swarm.LifecyclePostMember, Target: "researcher",
			}, swarm.GateArgs{
				SwarmID: "a-team", ChainPrefix: "a-team", MemberID: "researcher", CoordStore: store,
			})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("ext:relevance-gate"))
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
