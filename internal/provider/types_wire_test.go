package provider

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The wire-format contract: Message must serialise with lowercase
// role/content keys. The capitalised default ({"Role":...,
// "Content":...}) is rejected by the Z.ai API with error code 1214
// (observed live 2026-11-03).
var _ = Describe("provider.Message wire format", func() {
	It("serialises a message slice as lowercase role/content pairs", func() {
		b, err := json.Marshal([]Message{{Role: "user", Content: "hi"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(MatchJSON(`[{"role":"user","content":"hi"}]`))
	})

	It("serialises a single message with lowercase keys", func() {
		b, err := json.Marshal(Message{Role: "user", Content: "hi"})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(b)).To(MatchJSON(`{"role":"user","content":"hi"}`))
	})
})
