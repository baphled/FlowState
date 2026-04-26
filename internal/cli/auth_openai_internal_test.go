package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("isValidOpenAIKey", func() {
	DescribeTable("validates credential formats",
		func(key string, expected bool) {
			Expect(isValidOpenAIKey(key)).To(Equal(expected))
		},
		Entry("valid sk- key", "sk-abcdefghijklmnopqrstuvwxyz", true),
		Entry("valid sk-proj- key", "sk-proj-abcdefghijklmnop", true),
		Entry("valid sk-svcacct- key", "sk-svcacct-abcdefghijklmnop", true),
		Entry("empty string", "", false),
		Entry("too short", "sk-abc", false),
		Entry("missing sk- prefix", "abcdefghijklmnopqrstuvwxyz", false),
	)
})
