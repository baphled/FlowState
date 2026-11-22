package main

import (
	"os"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
