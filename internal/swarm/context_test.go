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
})
