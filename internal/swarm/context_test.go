package swarm_test

import (
	"context"

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
	})

	Describe("NotFoundError", func() {
		It("renders the spec §2 canonical message", func() {
			err := &swarm.NotFoundError{ID: "ghost"}

			Expect(err.Error()).To(Equal(`no agent or swarm named "ghost"`))
		})
	})
})
