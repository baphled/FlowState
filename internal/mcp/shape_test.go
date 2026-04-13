package mcp_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/mcp"
)

var _ = Describe("DecodeContent", func() {
	type payload struct {
		Chunks []string `json:"chunks"`
	}

	It("treats the literal 'undefined' as an empty, non-error result", func() {
		var got payload
		empty, err := mcp.DecodeContent("undefined", &got,
			"tool", "query_vault", "server", "vault-rag")
		Expect(err).ToNot(HaveOccurred())
		Expect(empty).To(BeTrue())
		Expect(got.Chunks).To(BeEmpty())
	})

	It("treats whitespace-only content as an empty, non-error result", func() {
		var got payload
		empty, err := mcp.DecodeContent("   \t\r\n  ", &got,
			"tool", "query_vault", "server", "vault-rag")
		Expect(err).ToNot(HaveOccurred())
		Expect(empty).To(BeTrue())
	})

	It("treats an empty string as an empty, non-error result", func() {
		var got payload
		empty, err := mcp.DecodeContent("", &got,
			"tool", "query_vault", "server", "vault-rag")
		Expect(err).ToNot(HaveOccurred())
		Expect(empty).To(BeTrue())
	})

	It("decodes a JSON object into the target", func() {
		var got payload
		empty, err := mcp.DecodeContent(`{"chunks":["a","b"]}`, &got,
			"tool", "query_vault", "server", "vault-rag")
		Expect(err).ToNot(HaveOccurred())
		Expect(empty).To(BeFalse())
		Expect(got.Chunks).To(Equal([]string{"a", "b"}))
	})

	It("decodes a JSON array into the target", func() {
		var got []string
		empty, err := mcp.DecodeContent(`["x","y"]`, &got,
			"tool", "query_vault", "server", "vault-rag")
		Expect(err).ToNot(HaveOccurred())
		Expect(empty).To(BeFalse())
		Expect(got).To(Equal([]string{"x", "y"}))
	})

	It("propagates a json.Unmarshal error when the content is JSON-shaped but malformed", func() {
		var got payload
		empty, err := mcp.DecodeContent(`{"chunks": [`, &got,
			"tool", "query_vault", "server", "vault-rag")
		Expect(err).To(HaveOccurred())
		Expect(empty).To(BeFalse())
	})
})
