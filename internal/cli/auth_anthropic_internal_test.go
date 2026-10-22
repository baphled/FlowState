package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("isValidAnthropicKey", func() {
	DescribeTable("validates credential formats",
		func(key string, expected bool) {
			Expect(isValidAnthropicKey(key)).To(Equal(expected))
		},
		Entry("valid API key", "sk-ant-api03-abcdefghijklmnopqrstuvwxyz1234567890", true),
		Entry("valid OAuth token", "sk-ant-oat01-abcdefghijklmnopqrstuvwxyz1234567890", true),
		Entry("empty string", "", false),
		Entry("too short", "sk-ant-api03", false),
		Entry("wrong prefix", "sk-ant-api02-abcdefghijklmnopqrstuvwxyz", false),
		Entry("completely invalid", "not-a-valid-key-at-all", false),
		Entry("partial prefix match", "sk-ant-api03", false),
	)
})
