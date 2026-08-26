package lifecycle_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine/lifecycle"
	"github.com/baphled/flowstate/internal/tool"
)

type stubTool struct{}

func (stubTool) Name() string        { return "stub" }
func (stubTool) Description() string { return "stub tool" }
func (stubTool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	return tool.Result{Output: "stubbed"}, nil
}
func (stubTool) Schema() tool.Schema { return tool.Schema{Type: "object"} }

var _ = Describe("PreToolExecCtx", func() {
	It("carries tool, args, and the skipped flag", func() {
		t := stubTool{}
		ctx := lifecycle.PreToolExecCtx{
			Tool:    t,
			Args:    map[string]any{"path": "/tmp/x"},
			Skipped: false,
		}
		Expect(ctx.Tool.Name()).To(Equal("stub"))
		Expect(ctx.Args).To(HaveKeyWithValue("path", "/tmp/x"))
		Expect(ctx.Skipped).To(BeFalse())
	})

	It("allows a hook to set Skipped and short-circuit dispatch", func() {
		t := stubTool{}
		base := lifecycle.PreToolExecCtx{Tool: t, Args: map[string]any{}}

		skipHook := func(ctx lifecycle.PreToolExecCtx, next func(lifecycle.PreToolExecCtx) (lifecycle.PreToolExecCtx, error)) (lifecycle.PreToolExecCtx, error) {
			ctx.Skipped = true
			return ctx, lifecycle.ErrTurnRejected
		}

		tl := lifecycle.DefaultTurnLifecycle()
		tl.AddPreToolExecHook(skipHook)

		out, err := tl.PreToolExec.Execute(base)
		Expect(err).To(MatchError(lifecycle.ErrTurnRejected))
		Expect(out.Skipped).To(BeTrue())
	})
})
