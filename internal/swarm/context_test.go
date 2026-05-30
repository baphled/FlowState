package swarm_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

// newTechTeamRegistry returns a real *swarm.Registry seeded with the
// tech-team manifest the resolver specs query against. Constructing
// it via NewRegistry + Register keeps the spec exercising the same
// surface production callers use.
func newTechTeamRegistry() *swarm.Registry {
	reg := swarm.NewRegistry()
	reg.Register(&swarm.Manifest{
		ID:      "tech-team",
		Lead:    "tech-lead",
		Members: []string{"explorer", "analyst"},
		Context: swarm.ContextConfig{ChainPrefix: "tech"},
	})
	return reg
}

var _ = Describe("swarm.Context", func() {
	Describe("NewContext", func() {
		It("copies fields from the manifest under the user-typed id", func() {
			m := &swarm.Manifest{
				ID:      "tech-team",
				Lead:    "tech-lead",
				Members: []string{"explorer", "analyst"},
				Context: swarm.ContextConfig{ChainPrefix: "tech"},
			}

			ctx := swarm.NewContext("tech-team", m)

			Expect(ctx.SwarmID).To(Equal("tech-team"))
			Expect(ctx.LeadAgent).To(Equal("tech-lead"))
			Expect(ctx.Members).To(Equal([]string{"explorer", "analyst"}))
			Expect(ctx.ChainPrefix).To(Equal("tech"))
			Expect(ctx.Gates).To(BeEmpty())
		})

		It("falls back to the swarm id when the manifest leaves ChainPrefix blank", func() {
			m := &swarm.Manifest{ID: "tech-team", Lead: "tech-lead"}

			ctx := swarm.NewContext("tech-team", m)

			Expect(ctx.ChainPrefix).To(Equal("tech-team"))
		})

		It("survives a nil manifest with the id-only zero envelope", func() {
			ctx := swarm.NewContext("orphan", nil)

			Expect(ctx.SwarmID).To(Equal("orphan"))
			Expect(ctx.LeadAgent).To(BeEmpty())
			Expect(ctx.Members).To(BeEmpty())
		})
	})

	Describe("AssignRunChainID", func() {
		// The engine assigns a per-run chainID at swarm start so the LLM can
		// no longer invent a free-form one (ADR - Engine-Owned Workflow
		// Mechanics, forward decision). When the manifest leaves chain_prefix
		// blank, NewContext defaults ChainPrefix to the swarm id — that is
		// the manifest-default the assignment replaces with a per-run
		// namespace. An explicitly pinned chain_prefix is honoured untouched
		// (backwards compat).
		It("derives a per-run namespace under the swarm id when the prefix is the manifest default", func() {
			m := &swarm.Manifest{ID: "planning-loop", Lead: "planner"}
			ctx := swarm.NewContext("planning-loop", m)
			Expect(ctx.ChainPrefix).To(Equal("planning-loop"),
				"precondition: manifest default prefix is the swarm id")

			ctx.AssignRunChainID("session-abc")

			Expect(ctx.ChainPrefix).NotTo(Equal("planning-loop"),
				"the per-run chainID replaces the static swarm-id default")
			Expect(ctx.ChainPrefix).To(HavePrefix("planning-loop-"),
				"the per-run namespace stays anchored under the swarm id")
		})

		It("is deterministic for the same run id", func() {
			a := swarm.NewContext("planning-loop", &swarm.Manifest{ID: "planning-loop", Lead: "planner"})
			b := swarm.NewContext("planning-loop", &swarm.Manifest{ID: "planning-loop", Lead: "planner"})

			a.AssignRunChainID("session-abc")
			b.AssignRunChainID("session-abc")

			Expect(a.ChainPrefix).To(Equal(b.ChainPrefix),
				"the same run id must yield the same engine-assigned chainID")
		})

		It("produces distinct namespaces for distinct runs of the same swarm", func() {
			a := swarm.NewContext("planning-loop", &swarm.Manifest{ID: "planning-loop", Lead: "planner"})
			b := swarm.NewContext("planning-loop", &swarm.Manifest{ID: "planning-loop", Lead: "planner"})

			a.AssignRunChainID("session-one")
			b.AssignRunChainID("session-two")

			Expect(a.ChainPrefix).NotTo(Equal(b.ChainPrefix),
				"two runs of the same swarm must not collide on coord-store keys")
		})

		It("honours an explicit manifest chain_prefix (backwards compat)", func() {
			m := &swarm.Manifest{
				ID:      "tech-team",
				Lead:    "tech-lead",
				Context: swarm.ContextConfig{ChainPrefix: "tech"},
			}
			ctx := swarm.NewContext("tech-team", m)

			ctx.AssignRunChainID("session-abc")

			Expect(ctx.ChainPrefix).To(Equal("tech"),
				"an explicitly pinned chain_prefix is the caller's choice and must not be overwritten")
		})

		It("is a no-op for an empty run id", func() {
			ctx := swarm.NewContext("planning-loop", &swarm.Manifest{ID: "planning-loop", Lead: "planner"})

			ctx.AssignRunChainID("")

			Expect(ctx.ChainPrefix).To(Equal("planning-loop"),
				"with no run id to derive from, the static default stands")
		})
	})

	Describe("AllowlistMembers", func() {
		It("returns a defensive copy that callers can mutate", func() {
			ctx := swarm.Context{Members: []string{"a", "b"}}

			allowlist := ctx.AllowlistMembers()
			allowlist[0] = "MUTATED"

			Expect(ctx.Members[0]).To(Equal("a"),
				"mutating the allowlist must not bleed back into the Context's Members slice")
		})

		It("returns an empty non-nil slice when Members is nil", func() {
			ctx := swarm.Context{}

			allowlist := ctx.AllowlistMembers()

			Expect(allowlist).NotTo(BeNil())
			Expect(allowlist).To(BeEmpty())
		})
	})

	Describe("WithContext / FromContext", func() {
		It("round-trips the carried context through context.Context values", func() {
			parent := context.Background()
			swarmCtx := swarm.Context{
				SwarmID:   "tech-team",
				LeadAgent: "tech-lead",
				Members:   []string{"analyst"},
			}

			derived := swarm.WithContext(parent, swarmCtx)

			got, ok := swarm.FromContext(derived)
			Expect(ok).To(BeTrue())
			Expect(got).NotTo(BeNil())
			Expect(got.SwarmID).To(Equal("tech-team"))
			Expect(got.LeadAgent).To(Equal("tech-lead"))
			Expect(got.Members).To(Equal([]string{"analyst"}))
		})

		It("returns (nil, false) on a context with no swarm value", func() {
			got, ok := swarm.FromContext(context.Background())

			Expect(ok).To(BeFalse())
			Expect(got).To(BeNil())
		})

		It("tolerates a nil context.Context", func() {
			//lint:ignore SA1012 intentionally passing nil to pin defensive nil-handling in FromContext.
			got, ok := swarm.FromContext(nil)

			Expect(ok).To(BeFalse())
			Expect(got).To(BeNil())
		})
	})

	Describe("WithScope / ScopeFromContext", func() {
		// Per-turn scope marker distinct from WithContext. The dispatcher
		// attaches the scope at every turn boundary — including no-swarm
		// turns (nil *Context) — so the delegate gate can read the
		// dispatch-time decision off ctx instead of the shared engine
		// state. This is the load-bearing primitive for the cross-session
		// swarm-context-leak fix (planner session
		// 39de3ab5-6173-4baf-9e20-7514a326bd3c).
		It("round-trips a non-nil swarm context as scoped=true", func() {
			parent := context.Background()
			swarmCtx := swarm.Context{
				SwarmID:   "planning-loop",
				LeadAgent: "planner",
				Members:   []string{"plan-writer"},
			}

			derived := swarm.WithScope(parent, &swarmCtx)

			got, scoped := swarm.ScopeFromContext(derived)
			Expect(scoped).To(BeTrue(), "WithScope marks the ctx as scoped")
			Expect(got).NotTo(BeNil())
			Expect(got.SwarmID).To(Equal("planning-loop"))
			Expect(got.Members).To(Equal([]string{"plan-writer"}))
		})

		It("round-trips a nil pointer as scoped=true with no swarm", func() {
			// Dispatcher attaches WithScope(ctx, nil) on no-swarm turns
			// so the gate can distinguish "this turn is standalone" from
			// "no dispatcher wired this ctx" (engine-state fallback).
			derived := swarm.WithScope(context.Background(), nil)

			got, scoped := swarm.ScopeFromContext(derived)
			Expect(scoped).To(BeTrue(),
				"WithScope(ctx, nil) still marks the ctx as scoped — the dispatcher made an explicit no-swarm decision")
			Expect(got).To(BeNil(),
				"the scoped value is nil so the gate skips the swarm-members branch and falls through to the static allowlist")
		})

		It("returns (nil, false) when no scope has been attached", func() {
			// Legacy callers / bare-engine test surfaces don't attach a
			// scope. ScopeFromContext signals scoped=false so the gate
			// can fall back to the engine-state lookup (preserves
			// pre-fix behaviour for tests that haven't migrated).
			got, scoped := swarm.ScopeFromContext(context.Background())

			Expect(scoped).To(BeFalse(),
				"no WithScope call → not scoped; gate falls back to engine state")
			Expect(got).To(BeNil())
		})

		It("tolerates a nil context.Context", func() {
			//lint:ignore SA1012 intentionally passing nil to pin defensive nil-handling.
			got, scoped := swarm.ScopeFromContext(nil)

			Expect(scoped).To(BeFalse())
			Expect(got).To(BeNil())
		})
	})

	Describe("Resolve", func() {
		var swarmReg *swarm.Registry

		BeforeEach(func() {
			swarmReg = newTechTeamRegistry()
		})

		It("resolves a known agent id to KindAgent without consulting the swarm registry", func() {
			hasAgent := func(id string) bool { return id == "explorer" }

			kind, manifest := swarm.Resolve("explorer", hasAgent, swarmReg)

			Expect(kind).To(Equal(swarm.KindAgent))
			Expect(manifest).To(BeNil())
		})

		It("falls through to the swarm registry on an agent miss", func() {
			hasAgent := func(_ string) bool { return false }

			kind, manifest := swarm.Resolve("tech-team", hasAgent, swarmReg)

			Expect(kind).To(Equal(swarm.KindSwarm))
			Expect(manifest).NotTo(BeNil())
			Expect(manifest.Lead).To(Equal("tech-lead"))
		})

		It("returns KindNone when neither registry knows the id", func() {
			hasAgent := func(_ string) bool { return false }

			kind, manifest := swarm.Resolve("ghost", hasAgent, swarmReg)

			Expect(kind).To(Equal(swarm.KindNone))
			Expect(manifest).To(BeNil())
		})

		It("returns KindNone for an empty id without touching either registry", func() {
			hasAgent := func(_ string) bool {
				Fail("hasAgent must not be called for an empty id")
				return false
			}

			kind, _ := swarm.Resolve("", hasAgent, swarmReg)

			Expect(kind).To(Equal(swarm.KindNone))
		})

		It("treats a nil agent lookup as empty", func() {
			kind, manifest := swarm.Resolve("tech-team", nil, swarmReg)

			Expect(kind).To(Equal(swarm.KindSwarm))
			Expect(manifest).NotTo(BeNil())
		})

		It("treats a nil swarm registry as empty", func() {
			hasAgent := func(_ string) bool { return false }

			kind, _ := swarm.Resolve("tech-team", hasAgent, nil)

			Expect(kind).To(Equal(swarm.KindNone))
		})

		// The auto-dispatch-on-lead branch exists so swarms whose lead is
		// an agent (e.g. `@coordinator` for the a-team swarm) can be
		// invoked by typing the lead's name and still have the swarm
		// runtime install — the Team-Lead regression on session
		// b62472a2-fa39-47a7-b049-e60f264260fe (184 messages, 173 bash
		// calls, zero delegate calls) proved the lead's prompt block
		// silently no-ops when KindAgent returns and the engine receives
		// no swarmCtx.
		Context("when an agent leads a swarm with AutoDispatchOnLead=true", func() {
			It("resolves to KindSwarm with that swarm's manifest", func() {
				reg := swarm.NewRegistry()
				reg.Register(&swarm.Manifest{
					ID:                 "a-team",
					Lead:               "coordinator",
					Members:            []string{"researcher", "writer"},
					AutoDispatchOnLead: true,
				})
				// "coordinator" is also a registered agent (the lead) —
				// auto-dispatch must beat the KindAgent verdict that the
				// hasAgent hit would otherwise produce.
				hasAgent := func(id string) bool { return id == "coordinator" }

				kind, manifest := swarm.Resolve("coordinator", hasAgent, reg)

				Expect(kind).To(Equal(swarm.KindSwarm))
				Expect(manifest).NotTo(BeNil())
				Expect(manifest.ID).To(Equal("a-team"))
				Expect(manifest.Lead).To(Equal("coordinator"))
			})
		})

		Context("when an agent leads a swarm with AutoDispatchOnLead=false", func() {
			It("preserves the KindAgent verdict for standalone invocation", func() {
				// Mirrors engineer-swarm: Senior-Engineer is the lead but
				// is also routinely invoked standalone for ad-hoc work
				// (sessions show 422/254/145-msg standalone runs). The
				// opt-out default must keep @Senior-Engineer resolving to
				// the agent, not the swarm.
				reg := swarm.NewRegistry()
				reg.Register(&swarm.Manifest{
					ID:                 "engineer-swarm",
					Lead:               "Senior-Engineer",
					Members:            []string{"Mid-Engineer", "Junior-Engineer"},
					AutoDispatchOnLead: false,
				})
				hasAgent := func(id string) bool { return id == "Senior-Engineer" }

				kind, manifest := swarm.Resolve("Senior-Engineer", hasAgent, reg)

				Expect(kind).To(Equal(swarm.KindAgent))
				Expect(manifest).To(BeNil())
			})
		})

		// Three-tier meta-swarm architecture (May 2026): the bundled
		// coordinator agent is now the lead of `meta-swarm` whose
		// members ARE OTHER SWARMS (a-team, dev-swarm, planning-loop,
		// board-room). The resolver must auto-dispatch meta-swarm —
		// NOT a-team — when the user invokes `@coordinator`. This is
		// the regression-pin spec for the rewiring: when only one of
		// the swarms in the registry has AutoDispatchOnLead=true on
		// `coordinator`, the resolver returns that swarm's manifest.
		//
		// See plan: Meta-Swarm Coordinator Architecture (May 2026).
		Context("when coordinator leads meta-swarm with AutoDispatchOnLead=true and a-team without it", func() {
			It("resolves @coordinator to the meta-swarm manifest", func() {
				reg := swarm.NewRegistry()
				// meta-swarm: opted in; members are SWARM ids.
				reg.Register(&swarm.Manifest{
					ID:                 "meta-swarm",
					Lead:               "coordinator",
					Members:            []string{"a-team", "dev-swarm", "planning-loop", "board-room"},
					AutoDispatchOnLead: true,
				})
				// a-team: still has coordinator as lead but opted OUT
				// of auto-dispatch in the meta-swarm world, so calling
				// @coordinator must NOT match a-team.
				reg.Register(&swarm.Manifest{
					ID:                 "a-team",
					Lead:               "coordinator",
					Members:            []string{"researcher", "writer"},
					AutoDispatchOnLead: false,
				})
				hasAgent := func(id string) bool { return id == "coordinator" }

				kind, manifest := swarm.Resolve("coordinator", hasAgent, reg)

				Expect(kind).To(Equal(swarm.KindSwarm))
				Expect(manifest).NotTo(BeNil())
				Expect(manifest.ID).To(Equal("meta-swarm"),
					"@coordinator must auto-dispatch meta-swarm now that coordinator is "+
						"a generic swarm orchestrator and meta-swarm leads sub-swarms")
				Expect(manifest.Members).To(ContainElements("a-team", "dev-swarm", "planning-loop", "board-room"))
			})
		})

		Context("when an agent leads multiple swarms that all have AutoDispatchOnLead=true", func() {
			It("returns KindAgent because the auto-dispatch target is ambiguous", func() {
				// Defensive case: today no agent leads more than one
				// swarm, but the resolver must fall back to KindAgent
				// when more than one auto-dispatch candidate exists so
				// the user is forced to invoke the desired swarm by id.
				reg := swarm.NewRegistry()
				reg.Register(&swarm.Manifest{
					ID:                 "swarm-one",
					Lead:               "shared-lead",
					Members:            []string{"a"},
					AutoDispatchOnLead: true,
				})
				reg.Register(&swarm.Manifest{
					ID:                 "swarm-two",
					Lead:               "shared-lead",
					Members:            []string{"b"},
					AutoDispatchOnLead: true,
				})
				hasAgent := func(id string) bool { return id == "shared-lead" }

				kind, manifest := swarm.Resolve("shared-lead", hasAgent, reg)

				Expect(kind).To(Equal(swarm.KindAgent))
				Expect(manifest).To(BeNil())
			})
		})
	})

	Describe("ResolveTarget", func() {
		var swarmReg *swarm.Registry

		BeforeEach(func() {
			swarmReg = newTechTeamRegistry()
		})

		It("returns the id verbatim with nil ctx for an agent target", func() {
			hasAgent := func(id string) bool { return id == "explorer" }

			leadID, ctx, err := swarm.ResolveTarget(hasAgent, swarmReg, "explorer")

			Expect(err).NotTo(HaveOccurred())
			Expect(leadID).To(Equal("explorer"))
			Expect(ctx).To(BeNil())
		})

		It("returns the lead id and a fresh *Context for a swarm target", func() {
			hasAgent := func(_ string) bool { return false }

			leadID, ctx, err := swarm.ResolveTarget(hasAgent, swarmReg, "tech-team")

			Expect(err).NotTo(HaveOccurred())
			Expect(leadID).To(Equal("tech-lead"))
			Expect(ctx).NotTo(BeNil())
			Expect(ctx.SwarmID).To(Equal("tech-team"))
			Expect(ctx.LeadAgent).To(Equal("tech-lead"))
		})

		It("returns *NotFoundError when neither registry knows the id", func() {
			hasAgent := func(_ string) bool { return false }

			leadID, ctx, err := swarm.ResolveTarget(hasAgent, swarmReg, "ghost")

			Expect(err).To(HaveOccurred())
			Expect(leadID).To(Equal(""))
			Expect(ctx).To(BeNil())
			var notFound *swarm.NotFoundError
			Expect(errors.As(err, &notFound)).To(BeTrue())
			Expect(notFound.ID).To(Equal("ghost"))
		})

		It("errors when the swarm manifest has an empty Lead", func() {
			leadlessReg := swarm.NewRegistry()
			leadlessReg.Register(&swarm.Manifest{ID: "leadless", Lead: "", Members: []string{"explorer"}})
			hasAgent := func(_ string) bool { return false }

			_, _, err := swarm.ResolveTarget(hasAgent, leadlessReg, "leadless")

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no lead agent"))
		})

		It("passes through (id, nil, nil) when hasAgent is nil — preserves the bare-engine CLI test contract", func() {
			leadID, ctx, err := swarm.ResolveTarget(nil, swarmReg, "anything")

			Expect(err).NotTo(HaveOccurred())
			Expect(leadID).To(Equal("anything"))
			Expect(ctx).To(BeNil())
		})

		It("passes through (id, nil, nil) when swarmReg is nil — same bare-engine contract", func() {
			hasAgent := func(_ string) bool { return false }

			leadID, ctx, err := swarm.ResolveTarget(hasAgent, nil, "anything")

			Expect(err).NotTo(HaveOccurred())
			Expect(leadID).To(Equal("anything"))
			Expect(ctx).To(BeNil())
		})
	})

	Describe("NotFoundError", func() {
		It("renders the spec §2 canonical message", func() {
			err := &swarm.NotFoundError{ID: "ghost"}

			Expect(err.Error()).To(Equal(`no agent or swarm named "ghost"`))
		})
	})

	// SlugifyChainID is the single key-safety boundary every chainID passes
	// through before becoming a coordination-store namespace. The recurring
	// planning-loop doom-loop was an LLM free-forming a chainID WITH A SLASH
	// ("planner/sme-sectional-plans"): members wrote evidence under
	// "planner/sme-sectional-plans/codebase-findings" (three path segments)
	// while the validator/publisher/gate split on the first "/" and never
	// found it. Slugifying renders any candidate key-safe so a value can
	// NEVER fracture "<chainID>/<suffix>" parsing.
	Describe("SlugifyChainID", func() {
		It("slugifies the exact LLM doom-loop repro (a chainID with a slash) to a key-safe value", func() {
			// The precise value that doom-looped a live run: a free-form
			// chainID with a slash. After slugifying it must contain no "/"
			// so "<chainID>/<suffix>" parsing resolves the whole chain as
			// ONE namespace segment.
			got := swarm.SlugifyChainID("planner/sme-sectional-plans")

			Expect(got).NotTo(ContainSubstring("/"),
				"a slugified chainID must never contain a path separator that fractures key parsing")
			Expect(got).To(Equal("planner-sme-sectional-plans"),
				"the slash collapses to a hyphen so the value stays one readable key segment")
		})

		It("collapses whitespace and back-slashes to a single hyphen", func() {
			Expect(swarm.SlugifyChainID("my chain id")).To(Equal("my-chain-id"))
			Expect(swarm.SlugifyChainID("a\\b")).To(Equal("a-b"))
			Expect(swarm.SlugifyChainID("a   b")).To(Equal("a-b"),
				"a run of whitespace collapses to a single separator")
		})

		It("drops other unsafe characters without leaving a separator", func() {
			Expect(swarm.SlugifyChainID("plan@auth#2026")).To(Equal("planauth2026"))
		})

		It("leaves the engine-assigned form unchanged (no-op on a safe value)", func() {
			// The {swarmID}-{hash} form AssignRunChainID stamps is already
			// within the safe alphabet, so the authoritative value survives.
			Expect(swarm.SlugifyChainID("planning-loop-ab12cd34ef56")).
				To(Equal("planning-loop-ab12cd34ef56"))
		})

		It("trims leading/trailing separators and dots", func() {
			Expect(swarm.SlugifyChainID("/leading/slash/")).To(Equal("leading-slash"))
			Expect(swarm.SlugifyChainID(".hidden")).To(Equal("hidden"),
				"a leading dot would otherwise produce a dotfile-style key segment")
		})

		It("returns empty for empty or all-unsafe input so the suffix-scan fallback still applies", func() {
			Expect(swarm.SlugifyChainID("")).To(BeEmpty())
			Expect(swarm.SlugifyChainID("///")).To(BeEmpty())
		})
	})

	// NormaliseMemberCoordKey is the WRITE-side counterpart of the gate's
	// READ-side key resolution. It rewrites a member's coord-store write key
	// so the chainID prefix is the engine-authoritative value, regardless of
	// what (often slashed) free-form value the lead dictated in its brief.
	Describe("NormaliseMemberCoordKey", func() {
		const chainID = "planning-loop-7d67530ef355"

		It("re-anchors a slashed free-form chainID onto the authoritative chainID", func() {
			// Live planning-loop session 836526fd: the lead free-formed
			// chainID=planning/planner and the explorer wrote
			// planning/planner/codebase-findings.
			Expect(swarm.NormaliseMemberCoordKey(chainID, "planning/planner/codebase-findings")).
				To(Equal(chainID + "/codebase-findings"))
		})

		It("recovers each single-segment member suffix from a drifted prefix", func() {
			Expect(swarm.NormaliseMemberCoordKey(chainID, "x/external-refs")).To(Equal(chainID + "/external-refs"))
			Expect(swarm.NormaliseMemberCoordKey(chainID, "x/analysis")).To(Equal(chainID + "/analysis"))
			Expect(swarm.NormaliseMemberCoordKey(chainID, "x/y/plan")).To(Equal(chainID + "/plan"))
			Expect(swarm.NormaliseMemberCoordKey(chainID, "x/review")).To(Equal(chainID + "/review"))
			Expect(swarm.NormaliseMemberCoordKey(chainID, "x/requirements")).To(Equal(chainID + "/requirements"))
			Expect(swarm.NormaliseMemberCoordKey(chainID, "x/interview")).To(Equal(chainID + "/interview"))
		})

		It("prefixes a bare suffix-only key", func() {
			Expect(swarm.NormaliseMemberCoordKey(chainID, "codebase-findings")).To(Equal(chainID + "/codebase-findings"))
		})

		It("preserves multi-segment section suffixes (sub-swarm)", func() {
			Expect(swarm.NormaliseMemberCoordKey(chainID, "bogus/sections/architecture")).
				To(Equal(chainID + "/sections/architecture"))
			Expect(swarm.NormaliseMemberCoordKey(chainID, "planning/planner/sections/testing")).
				To(Equal(chainID + "/sections/testing"))
			Expect(swarm.NormaliseMemberCoordKey(chainID, "sections/security")).
				To(Equal(chainID + "/sections/security"))
		})

		It("is a no-op when the key is already correctly prefixed", func() {
			Expect(swarm.NormaliseMemberCoordKey(chainID, chainID+"/codebase-findings")).
				To(Equal(chainID + "/codebase-findings"))
			Expect(swarm.NormaliseMemberCoordKey(chainID, chainID+"/sections/architecture")).
				To(Equal(chainID + "/sections/architecture"))
		})

		It("replaces the first segment for an unknown suffix (safe degrade)", func() {
			Expect(swarm.NormaliseMemberCoordKey(chainID, "bogus/custom-key")).To(Equal(chainID + "/custom-key"))
			Expect(swarm.NormaliseMemberCoordKey(chainID, "lone-key")).To(Equal(chainID + "/lone-key"))
		})

		It("leaves the key untouched when no authoritative chainID is supplied", func() {
			Expect(swarm.NormaliseMemberCoordKey("", "planning/planner/codebase-findings")).
				To(Equal("planning/planner/codebase-findings"))
		})
	})

	Describe("MemberCoordChainID", func() {
		It("returns the slugified authoritative chainID inside an engine-owned swarm", func() {
			sc := &swarm.Context{SwarmID: "planning-loop"}
			sc.ChainPrefix = "planning-loop-7d67530ef355"
			sc.ChainIDAssigned = true
			ctx := swarm.WithScope(context.Background(), sc)
			Expect(swarm.MemberCoordChainID(ctx)).To(Equal("planning-loop-7d67530ef355"))
		})

		It("returns empty when the chainID is not engine-assigned", func() {
			sc := &swarm.Context{SwarmID: "planning-loop", ChainPrefix: "planning-loop"}
			ctx := swarm.WithScope(context.Background(), sc)
			Expect(swarm.MemberCoordChainID(ctx)).To(BeEmpty())
		})

		It("returns empty for a standalone (no-scope) context", func() {
			Expect(swarm.MemberCoordChainID(context.Background())).To(BeEmpty())
		})

		It("returns empty for an explicit standalone (nil scope) marker", func() {
			ctx := swarm.WithScope(context.Background(), nil)
			Expect(swarm.MemberCoordChainID(ctx)).To(BeEmpty())
		})
	})
})
