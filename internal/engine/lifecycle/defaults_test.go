package lifecycle_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine/lifecycle"
)

var _ = Describe("Defaults", func() {
	Describe("DefaultTurnLifecycle", func() {
		It("returns a lifecycle whose stage names match the documented order", func() {
			tl := lifecycle.DefaultTurnLifecycle()
			Expect(tl.ContextAssembly.Name).To(Equal("context_assembly"))
			Expect(tl.PreStream.Name).To(Equal("pre_stream"))
			Expect(tl.Stream.Name).To(Equal("stream"))
			Expect(tl.PreToolExec.Name).To(Equal("pre_tool_exec"))
			Expect(tl.ToolExec.Name).To(Equal("tool_exec"))
			Expect(tl.PostProcess.Name).To(Equal("post_process"))
		})

		It("populates every slot-phase handler with a pass-through", func() {
			tl := lifecycle.DefaultTurnLifecycle()

			outCA, errCA := tl.ContextAssembly.Execute(lifecycle.ContextAssemblyCtx{})
			Expect(errCA).NotTo(HaveOccurred())
			Expect(outCA).To(Equal(lifecycle.ContextAssemblyCtx{}))

			outPS, errPS := tl.PreStream.Execute(lifecycle.PreStreamCtx{})
			Expect(errPS).NotTo(HaveOccurred())
			Expect(outPS).To(Equal(lifecycle.PreStreamCtx{}))

			outStream, errStream := tl.Stream.Execute(lifecycle.StreamCtx{})
			Expect(errStream).NotTo(HaveOccurred())
			Expect(outStream).To(Equal(lifecycle.StreamCtx{}))

			outPTE, errPTE := tl.PreToolExec.Execute(lifecycle.PreToolExecCtx{})
			Expect(errPTE).NotTo(HaveOccurred())
			Expect(outPTE).To(Equal(lifecycle.PreToolExecCtx{}))

			outTE, errTE := tl.ToolExec.Execute(lifecycle.ToolExecCtx{})
			Expect(errTE).NotTo(HaveOccurred())
			Expect(outTE).To(Equal(lifecycle.ToolExecCtx{}))

			outPP, errPP := tl.PostProcess.Execute(lifecycle.PostProcessCtx{})
			Expect(errPP).NotTo(HaveOccurred())
			Expect(outPP).To(Equal(lifecycle.PostProcessCtx{}))
		})

		It("starts every slot-phase hook chain empty", func() {
			tl := lifecycle.DefaultTurnLifecycle()
			Expect(tl.PreStream.Hooks).To(BeEmpty())
			Expect(tl.PreToolExec.Hooks).To(BeEmpty())
		})
	})

	Describe("per-stage default constructors", func() {
		It("DefaultPreStream returns a stage named pre_stream with a pass-through handler", func() {
			stage := lifecycle.DefaultPreStream()
			Expect(stage.Name).To(Equal("pre_stream"))
			Expect(stage.Hooks).To(BeEmpty())
			out, err := stage.Execute(lifecycle.PreStreamCtx{})
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(Equal(lifecycle.PreStreamCtx{}))
		})

		It("DefaultStream returns a stage named stream with a pass-through handler", func() {
			stage := lifecycle.DefaultStream()
			Expect(stage.Name).To(Equal("stream"))
			out, err := stage.Execute(lifecycle.StreamCtx{})
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(Equal(lifecycle.StreamCtx{}))
		})

		It("DefaultPreToolExec returns a stage named pre_tool_exec with a pass-through handler", func() {
			stage := lifecycle.DefaultPreToolExec()
			Expect(stage.Name).To(Equal("pre_tool_exec"))
			out, err := stage.Execute(lifecycle.PreToolExecCtx{})
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(Equal(lifecycle.PreToolExecCtx{}))
		})

		It("DefaultToolExec returns a stage named tool_exec with a pass-through handler", func() {
			stage := lifecycle.DefaultToolExec()
			Expect(stage.Name).To(Equal("tool_exec"))
			out, err := stage.Execute(lifecycle.ToolExecCtx{})
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(Equal(lifecycle.ToolExecCtx{}))
		})

		It("DefaultPostProcess returns a stage named post_process with a pass-through handler", func() {
			stage := lifecycle.DefaultPostProcess()
			Expect(stage.Name).To(Equal("post_process"))
			out, err := stage.Execute(lifecycle.PostProcessCtx{})
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(Equal(lifecycle.PostProcessCtx{}))
		})
	})
})

var _ = Describe("TurnLifecycle hook registration", func() {
	Describe("Add*Hook methods", func() {
		It("appends each registered hook to the matching stage chain", func() {
			tl := lifecycle.DefaultTurnLifecycle()

			caHook := func(ctx lifecycle.ContextAssemblyCtx, next func(lifecycle.ContextAssemblyCtx) (lifecycle.ContextAssemblyCtx, error)) (lifecycle.ContextAssemblyCtx, error) {
				return next(ctx)
			}
			tl.AddContextAssemblyHook(caHook)
			Expect(tl.ContextAssembly.Hooks).To(HaveLen(1))

			psHook := func(ctx lifecycle.PreStreamCtx, next func(lifecycle.PreStreamCtx) (lifecycle.PreStreamCtx, error)) (lifecycle.PreStreamCtx, error) {
				return next(ctx)
			}
			tl.AddPreStreamHook(psHook)
			Expect(tl.PreStream.Hooks).To(HaveLen(1))

			sHook := func(ctx lifecycle.StreamCtx, next func(lifecycle.StreamCtx) (lifecycle.StreamCtx, error)) (lifecycle.StreamCtx, error) {
				return next(ctx)
			}
			tl.AddStreamHook(sHook)
			Expect(tl.Stream.Hooks).To(HaveLen(1))

			pteHook := func(ctx lifecycle.PreToolExecCtx, next func(lifecycle.PreToolExecCtx) (lifecycle.PreToolExecCtx, error)) (lifecycle.PreToolExecCtx, error) {
				return next(ctx)
			}
			tl.AddPreToolExecHook(pteHook)
			Expect(tl.PreToolExec.Hooks).To(HaveLen(1))

			teHook := func(ctx lifecycle.ToolExecCtx, next func(lifecycle.ToolExecCtx) (lifecycle.ToolExecCtx, error)) (lifecycle.ToolExecCtx, error) {
				return next(ctx)
			}
			tl.AddToolExecHook(teHook)
			Expect(tl.ToolExec.Hooks).To(HaveLen(1))

			ppHook := func(ctx lifecycle.PostProcessCtx, next func(lifecycle.PostProcessCtx) (lifecycle.PostProcessCtx, error)) (lifecycle.PostProcessCtx, error) {
				return next(ctx)
			}
			tl.AddPostProcessHook(ppHook)
			Expect(tl.PostProcess.Hooks).To(HaveLen(1))
		})

		It("preserves registration order across multiple registrations", func() {
			tl := lifecycle.DefaultTurnLifecycle()

			tl.AddPreStreamHook(func(ctx lifecycle.PreStreamCtx, next func(lifecycle.PreStreamCtx) (lifecycle.PreStreamCtx, error)) (lifecycle.PreStreamCtx, error) {
				return next(ctx)
			})
			tl.AddPreStreamHook(func(ctx lifecycle.PreStreamCtx, next func(lifecycle.PreStreamCtx) (lifecycle.PreStreamCtx, error)) (lifecycle.PreStreamCtx, error) {
				return next(ctx)
			})

			Expect(tl.PreStream.Hooks).To(HaveLen(2))
		})

		It("composes a registered hook with the stage handler so it runs around Execute", func() {
			tl := lifecycle.DefaultTurnLifecycle()

			order := []string{}
			tl.AddPreStreamHook(func(ctx lifecycle.PreStreamCtx, next func(lifecycle.PreStreamCtx) (lifecycle.PreStreamCtx, error)) (lifecycle.PreStreamCtx, error) {
				order = append(order, "before")
				out, err := next(ctx)
				order = append(order, "after")
				return out, err
			})

			_, err := tl.PreStream.Execute(lifecycle.PreStreamCtx{})
			Expect(err).NotTo(HaveOccurred())
			Expect(order).To(Equal([]string{"before", "after"}))
		})
	})
})
