package lifecycle_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine/lifecycle"
	"github.com/baphled/flowstate/internal/tool"
)

var _ = Describe("ToolExecCtx", func() {
	Describe("field carry-through", func() {
		It("carries caller identity, tool, args, and correlation id", func() {
			t := stubTool{}
			ctx := lifecycle.ToolExecCtx{
				Context:            context.Background(),
				SessionID:          "sess-1",
				ToolCallID:         "call-9",
				Tool:               t,
				Args:               map[string]any{"k": "v"},
				InternalToolCallID: "internal-77",
			}
			Expect(ctx.SessionID).To(Equal("sess-1"))
			Expect(ctx.ToolCallID).To(Equal("call-9"))
			Expect(ctx.Tool.Name()).To(Equal("stub"))
			Expect(ctx.Args).To(HaveKeyWithValue("k", "v"))
			Expect(ctx.InternalToolCallID).To(Equal("internal-77"))
		})

		It("starts with a nil Result and a nil Error", func() {
			var ctx lifecycle.ToolExecCtx
			Expect(ctx.Result).To(BeNil())
			Expect(ctx.Error).ToNot(HaveOccurred())
		})
	})

	Describe("handler propagation through Execute", func() {
		It("propagates Result set by the handler back to the caller", func() {
			t := stubTool{}
			result := &tool.Result{Output: "done"}
			stage := lifecycle.LifecycleStage[lifecycle.ToolExecCtx]{
				Name: "tool_exec",
				Handler: func(ctx lifecycle.ToolExecCtx) (lifecycle.ToolExecCtx, error) {
					ctx.Result = result
					return ctx, nil
				},
			}
			out, err := stage.Execute(lifecycle.ToolExecCtx{Tool: t})
			Expect(err).NotTo(HaveOccurred())
			Expect(out.Result).To(BeIdenticalTo(result))
		})

		It("propagates Error set by the handler back to the caller", func() {
			toolErr := errors.New("boom")
			stage := lifecycle.LifecycleStage[lifecycle.ToolExecCtx]{
				Name: "tool_exec",
				Handler: func(ctx lifecycle.ToolExecCtx) (lifecycle.ToolExecCtx, error) {
					return ctx, toolErr
				},
			}
			out, err := stage.Execute(lifecycle.ToolExecCtx{})
			Expect(err).To(MatchError(toolErr))
			Expect(out.Error).ToNot(HaveOccurred())
		})

		It("propagates Phase transitions applied by a hook", func() {
			advancing := func(ctx lifecycle.ToolExecCtx, next func(lifecycle.ToolExecCtx) (lifecycle.ToolExecCtx, error)) (lifecycle.ToolExecCtx, error) {
				ctx.Phase = lifecycle.ToolExecDispatch
				return next(ctx)
			}

			tl := lifecycle.DefaultTurnLifecycle()
			tl.AddToolExecHook(advancing)

			out, err := tl.ToolExec.Execute(lifecycle.ToolExecCtx{Phase: lifecycle.ToolExecBegin})
			Expect(err).NotTo(HaveOccurred())
			Expect(out.Phase).To(Equal(lifecycle.ToolExecDispatch))
		})
	})
})

var _ = Describe("ToolExecPhase constants", func() {
	It("exposes the four documented sub-lifecycle phases", func() {
		Expect(lifecycle.ToolExecBegin).To(Equal(lifecycle.ToolExecPhase("begin")))
		Expect(lifecycle.ToolExecDispatch).To(Equal(lifecycle.ToolExecPhase("dispatch")))
		Expect(lifecycle.ToolExecRun).To(Equal(lifecycle.ToolExecPhase("run")))
		Expect(lifecycle.ToolExecEnd).To(Equal(lifecycle.ToolExecPhase("end")))
	})

	It("keeps the four phases distinct", func() {
		phases := []lifecycle.ToolExecPhase{
			lifecycle.ToolExecBegin,
			lifecycle.ToolExecDispatch,
			lifecycle.ToolExecRun,
			lifecycle.ToolExecEnd,
		}
		Expect(phases).To(ContainElements(
			lifecycle.ToolExecPhase("begin"),
			lifecycle.ToolExecPhase("dispatch"),
			lifecycle.ToolExecPhase("run"),
			lifecycle.ToolExecPhase("end"),
		))
		seen := map[lifecycle.ToolExecPhase]bool{}
		for _, p := range phases {
			Expect(seen[p]).To(BeFalse(), "duplicate phase: "+string(p))
			seen[p] = true
		}
	})
})

var _ = Describe("ToolExecResult", func() {
	It("carries the tool call, result, and error together", func() {
		res := lifecycle.ToolExecResult{}
		Expect(res.Err).ToNot(HaveOccurred())
		Expect(res.ToolResult.Output).To(BeEmpty())
	})
})
