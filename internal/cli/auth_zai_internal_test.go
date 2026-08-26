package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("isValidZAIKey", func() {
	DescribeTable("validates credential formats",
		func(key string, expected bool) {
			Expect(isValidZAIKey(key)).To(Equal(expected))
		},
		Entry("valid 32-char key", "abcdefghijklmnopqrstuvwxyz123456", true),
		Entry("valid 16-char minimum", "abcdefghijklmnop", true),
		Entry("empty string", "", false),
		Entry("too short", "short-key", false),
	)
})
