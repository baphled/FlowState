package qdrant_test

import (
	"encoding/json"

	"github.com/baphled/flowstate/internal/recall/qdrant"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ScoredPoint JSON decoding", func() {
	It("decodes an integer id (legacy mem0/OpenCode writes) without error", func() {
		raw := []byte(`{"id":1776075781962028658,"score":0.9,"payload":{"content":"x"}}`)
		var p qdrant.ScoredPoint
		Expect(json.Unmarshal(raw, &p)).To(Succeed())
		Expect(p.ID).To(Equal("1776075781962028658"))
		Expect(p.Score).To(BeNumerically("~", 0.9, 0.001))
		Expect(p.Payload).To(HaveKeyWithValue("content", "x"))
	})

	It("decodes a UUID-string id (FlowState-native writes) without error", func() {
		raw := []byte(`{"id":"11111111-2222-5333-8444-555555555555","score":0.8,"payload":{}}`)
		var p qdrant.ScoredPoint
		Expect(json.Unmarshal(raw, &p)).To(Succeed())
		Expect(p.ID).To(Equal("11111111-2222-5333-8444-555555555555"))
		Expect(p.Score).To(BeNumerically("~", 0.8, 0.001))
	})

	It("decodes an array of mixed-id ScoredPoints", func() {
		raw := []byte(`[{"id":123,"score":0.5,"payload":{}},{"id":"abc-xyz","score":0.4,"payload":{}}]`)
		var pts []qdrant.ScoredPoint
		Expect(json.Unmarshal(raw, &pts)).To(Succeed())
		Expect(pts).To(HaveLen(2))
		Expect(pts[0].ID).To(Equal("123"))
		Expect(pts[1].ID).To(Equal("abc-xyz"))
	})

	It("returns a clear error for a non-scalar id (object/array)", func() {
		raw := []byte(`{"id":{"nested":"bad"},"score":0.1}`)
		var p qdrant.ScoredPoint
		err := json.Unmarshal(raw, &p)
		Expect(err).To(HaveOccurred())
	})
})
