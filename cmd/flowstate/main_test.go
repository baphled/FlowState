package main

import (
	"context"
	"os"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
)

var _ = Describe("signals that cancel a run", func() {
	notIgnored := func(os.Signal) bool { return false }
	hangUpIgnored := func(sig os.Signal) bool { return sig == syscall.SIGHUP }

	It("includes a hang-up when the process did not start with it ignored", func() {
		Expect(cancelSignals(notIgnored)).To(ContainElement(syscall.SIGHUP))
	})

	It("leaves a hang-up alone when the process started with it ignored, as under nohup", func() {
		Expect(cancelSignals(hangUpIgnored)).NotTo(ContainElement(syscall.SIGHUP))
	})

	DescribeTable("always includes the interrupt and termination signals",
		func(ignored func(os.Signal) bool, sig os.Signal) {
			Expect(cancelSignals(ignored)).To(ContainElement(sig))
		},
		Entry("interrupt", notIgnored, os.Interrupt),
		Entry("termination", notIgnored, syscall.SIGTERM),
		Entry("interrupt under nohup", hangUpIgnored, os.Interrupt),
		Entry("termination under nohup", hangUpIgnored, syscall.SIGTERM),
	)
})

var _ = Describe("watching signals for a cause", func() {
	var (
		ch      chan os.Signal
		ctx     context.Context
		stop    context.CancelFunc
		release int
	)

	BeforeEach(func() {
		ch = make(chan os.Signal, 1)
		release = 0
		ctx, stop = watchSignals(context.Background(), ch, func() { release++ })
		DeferCleanup(func() { stop() })
	})

	DescribeTable("cancels the context naming the signal",
		func(sig os.Signal, want string) {
			ch <- sig
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
				panic("signal did not cancel the context")
			}
			Expect(app.CancelReason(ctx)).To(Equal(want))
		},
		Entry("interrupt", os.Interrupt, "sigint"),
		Entry("termination", syscall.SIGTERM, "sigterm"),
		Entry("hang-up", syscall.SIGHUP, "sighup"),
	)

	It("stays live until a signal arrives", func() {
		Expect(ctx.Err()).NotTo(HaveOccurred())
	})

	It("releases the registration when stopped", func() {
		stop()
		Expect(release).To(Equal(1))
	})

	It("cancels without a signal cause when stopped", func() {
		stop()
		Expect(app.CancelReason(ctx)).To(Equal("cancelled"))
	})
})
