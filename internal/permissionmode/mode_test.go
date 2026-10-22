package permissionmode_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/permissionmode"
)

func TestPermissionmode(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Permissionmode Suite")
}

var _ = Describe("Mode constants", func() {
	It("exposes the five canonical permission modes", func() {
		Expect(permissionmode.ModePlan).To(Equal(permissionmode.Mode("plan")))
		Expect(permissionmode.ModeDefault).To(Equal(permissionmode.Mode("default")))
		Expect(permissionmode.ModeAcceptEdits).To(Equal(permissionmode.Mode("accept_edits")))
		Expect(permissionmode.ModeAskUser).To(Equal(permissionmode.Mode("ask")))
		Expect(permissionmode.ModeYolo).To(Equal(permissionmode.Mode("yolo")))
	})

	It("keeps all five mode constants distinct", func() {
		modes := []permissionmode.Mode{
			permissionmode.ModePlan,
			permissionmode.ModeDefault,
			permissionmode.ModeAcceptEdits,
			permissionmode.ModeAskUser,
			permissionmode.ModeYolo,
		}
		seen := map[permissionmode.Mode]bool{}
		for _, m := range modes {
			Expect(seen[m]).To(BeFalse(), "duplicate mode: "+m)
			seen[m] = true
		}
		Expect(seen).To(HaveLen(5))
	})
})

var _ = Describe("WithMode", func() {
	It("returns a derived context carrying the supplied mode", func() {
		ctx := permissionmode.WithMode(context.Background(), permissionmode.ModeYolo)
		Expect(permissionmode.FromContext(ctx)).To(Equal(permissionmode.ModeYolo))
	})

	It("returns the input context unchanged when mode is the empty string", func() {
		bg := context.Background()
		ctx := permissionmode.WithMode(bg, "")
		Expect(ctx).To(BeIdenticalTo(bg))
		Expect(permissionmode.FromContext(ctx)).To(Equal(permissionmode.ModeDefault))
	})

	It("overwrites a previously bound mode when called twice", func() {
		ctx := permissionmode.WithMode(context.Background(), permissionmode.ModePlan)
		ctx = permissionmode.WithMode(ctx, permissionmode.ModeAcceptEdits)
		Expect(permissionmode.FromContext(ctx)).To(Equal(permissionmode.ModeAcceptEdits))
	})

	DescribeTable("round-trips every canonical mode",
		func(mode permissionmode.Mode) {
			ctx := permissionmode.WithMode(context.Background(), mode)
			Expect(permissionmode.FromContext(ctx)).To(Equal(mode))
		},
		Entry("plan", permissionmode.ModePlan),
		Entry("default", permissionmode.ModeDefault),
		Entry("accept_edits", permissionmode.ModeAcceptEdits),
		Entry("ask", permissionmode.ModeAskUser),
		Entry("yolo", permissionmode.ModeYolo),
	)
})

var _ = Describe("FromContext", func() {
	It("returns ModeDefault when no mode has been bound", func() {
		Expect(permissionmode.FromContext(context.Background())).To(Equal(permissionmode.ModeDefault))
	})

	It("returns ModeDefault when ctx is nil", func() {
		// SA1012: pass a typed nil instead of the bare nil literal
		var nilCtx context.Context
		Expect(permissionmode.FromContext(nilCtx)).To(Equal(permissionmode.ModeDefault))
	})

	It("returns ModeDefault when the bound value is not a Mode", func() {
		// SA1029: define a local key type rather than using bare struct{}
		type testKey struct{}
		ctx := context.WithValue(context.Background(), testKey{}, "not-a-mode")
		Expect(permissionmode.FromContext(ctx)).To(Equal(permissionmode.ModeDefault))
	})
})

var _ = Describe("MutatingTools", func() {
	DescribeTable("classifies the documented mutating tool names",
		func(name string, mutating bool) {
			Expect(permissionmode.IsMutating(name)).To(Equal(mutating))
		},
		Entry("bash is mutating", "bash", true),
		Entry("write is mutating", "write", true),
		Entry("edit is mutating", "edit", true),
		Entry("multiedit is mutating", "multiedit", true),
		Entry("apply_patch is mutating", "apply_patch", true),
		Entry("read is not mutating", "read", false),
		Entry("unknown tool is not mutating", "nope", false),
		Entry("empty string is not mutating", "", false),
	)

	It("contains exactly the five documented mutating tools", func() {
		Expect(permissionmode.MutatingTools).To(HaveLen(5))
		Expect(permissionmode.MutatingTools).To(HaveKey("bash"))
		Expect(permissionmode.MutatingTools).To(HaveKey("write"))
		Expect(permissionmode.MutatingTools).To(HaveKey("edit"))
		Expect(permissionmode.MutatingTools).To(HaveKey("multiedit"))
		Expect(permissionmode.MutatingTools).To(HaveKey("apply_patch"))
	})
})

var _ = Describe("PlanModeStrippedTools", func() {
	DescribeTable("classifies the plan-mode stripped tool names",
		func(name string, stripped bool) {
			Expect(permissionmode.IsStrippedUnderPlan(name)).To(Equal(stripped))
		},
		Entry("bash is stripped", "bash", true),
		Entry("write is not stripped", "write", false),
		Entry("edit is not stripped", "edit", false),
		Entry("multiedit is not stripped", "multiedit", false),
		Entry("apply_patch is not stripped", "apply_patch", false),
		Entry("read is not stripped", "read", false),
		Entry("unknown tool is not stripped", "nope", false),
	)

	It("contains exactly bash", func() {
		Expect(permissionmode.PlanModeStrippedTools).To(HaveLen(1))
		Expect(permissionmode.PlanModeStrippedTools).To(HaveKey("bash"))
	})

	It("is a strict subset of MutatingTools", func() {
		for name := range permissionmode.PlanModeStrippedTools {
			Expect(permissionmode.IsMutating(name)).To(BeTrue(), "stripped tool not in MutatingTools: "+name)
		}
	})
})
