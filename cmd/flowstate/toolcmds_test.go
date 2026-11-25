package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("session tool against a live holder", func() {
	var id string
	var res result

	BeforeEach(func() {
		id = newTestSession()
		holdLease(id)
		res = runTool(id, "--tool", "echo", "--tool-arg", `{"a":1}`, "--allow", "echo")
	})

	It("exits 9", func() { Expect(res.exit).To(Equal(exitSessionBusy)) })
	It("reports the session_busy code", func() { Expect(stderrCode(res.stderr)).To(Equal("session_busy")) })
	It("writes no tool event", func() { Expect(runCLIEvents(id).stdout).NotTo(ContainSubstring("tool.call")) })
})
