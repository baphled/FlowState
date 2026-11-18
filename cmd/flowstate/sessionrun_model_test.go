package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("session run --model flag plumbing", func() {
	It("assigns the --model value to the run model override", func() {
		fl, _, err := parseFlags([]string{"--model", "glm-4.5-flash", "sess-1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(fl.model).To(Equal("glm-4.5-flash"))
	})

	It("keeps the positional session id when --model is given", func() {
		_, pos, err := parseFlags([]string{"--model=glm-4.5-flash", "sess-1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(pos).To(Equal([]string{"sess-1"}))
	})
})
