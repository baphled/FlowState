package api_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/session"
)

var _ = Describe("NewSessionResponse isStreaming field", func() {
	It("emits isStreaming: false when the session is not actively streaming", func() {
		sess := &session.Session{ID: "sess-streaming-false", AgentID: "agent-a"}

		resp := api.NewSessionResponse(sess)
		Expect(resp).NotTo(BeNil())
		Expect(resp.IsStreaming).To(BeFalse())

		raw, err := json.Marshal(resp)
		Expect(err).NotTo(HaveOccurred())

		var out map[string]interface{}
		Expect(json.Unmarshal(raw, &out)).To(Succeed())
		Expect(out).To(HaveKey("isStreaming"),
			"frontend needs isStreaming to detect active sessions on page load")
		Expect(out["isStreaming"]).To(BeFalse())
	})

	It("emits isStreaming: true when WithIsStreaming option is passed", func() {
		sess := &session.Session{ID: "sess-streaming-true", AgentID: "agent-a"}

		resp := api.NewSessionResponse(sess, api.WithIsStreaming(true))
		Expect(resp).NotTo(BeNil())
		Expect(resp.IsStreaming).To(BeTrue())

		raw, err := json.Marshal(resp)
		Expect(err).NotTo(HaveOccurred())

		var out map[string]interface{}
		Expect(json.Unmarshal(raw, &out)).To(Succeed())
		Expect(out).To(HaveKey("isStreaming"))
		Expect(out["isStreaming"]).To(BeTrue())
	})
})

var _ = Describe("NewSessionResponse model+provider projection", func() {
	It("projects CurrentModelID and CurrentProviderID into camelCase JSON keys", func() {
		sess := &session.Session{
			ID:                "sess-1",
			AgentID:           "agent-a",
			CurrentModelID:    "claude-opus-4.7",
			CurrentProviderID: "anthropic",
		}

		resp := api.NewSessionResponse(sess)
		Expect(resp).NotTo(BeNil())
		Expect(resp.CurrentModelID).To(Equal("claude-opus-4.7"))
		Expect(resp.CurrentProviderID).To(Equal("anthropic"))

		raw, err := json.Marshal(resp)
		Expect(err).NotTo(HaveOccurred())

		var out map[string]interface{}
		Expect(json.Unmarshal(raw, &out)).To(Succeed())
		Expect(out).To(HaveKeyWithValue("currentModelId", "claude-opus-4.7"))
		Expect(out).To(HaveKeyWithValue("currentProviderId", "anthropic"))
		Expect(out).NotTo(HaveKey("current_model_id"))
		Expect(out).NotTo(HaveKey("current_provider_id"))
	})

	It("omits currentModelId and currentProviderId when both are empty", func() {
		sess := &session.Session{ID: "sess-2", AgentID: "agent-a"}

		raw, err := json.Marshal(api.NewSessionResponse(sess))
		Expect(err).NotTo(HaveOccurred())

		var out map[string]interface{}
		Expect(json.Unmarshal(raw, &out)).To(Succeed())
		Expect(out).NotTo(HaveKey("currentModelId"))
		Expect(out).NotTo(HaveKey("currentProviderId"))
	})
})

var _ = Describe("NewSessionResponse chainId projection", func() {
	// Parity with Summary.ChainID (manager.go:167). Persisted as
	// Session.ChainID by 40ad53d2 to close the cold-reload hole on the
	// list endpoint; the single-session DTO surfaces the same field so
	// any caller that reads SessionResponse (POST /messages, PATCH /agent,
	// PATCH /model, future GET /sessions/{id}) sees the same data.
	It("projects ChainID into the chainId JSON key", func() {
		sess := &session.Session{
			ID:      "sess-chain",
			AgentID: "agent-a",
			ChainID: "chain-xyz",
		}

		resp := api.NewSessionResponse(sess)
		Expect(resp).NotTo(BeNil())
		Expect(resp.ChainID).To(Equal("chain-xyz"))

		raw, err := json.Marshal(resp)
		Expect(err).NotTo(HaveOccurred())

		var out map[string]interface{}
		Expect(json.Unmarshal(raw, &out)).To(Succeed())
		Expect(out).To(HaveKeyWithValue("chainId", "chain-xyz"))
		Expect(out).NotTo(HaveKey("chain_id"),
			"snake_case is the persistence shape; wire is camelCase")
	})

	It("omits chainId when the session has no chain (root session)", func() {
		sess := &session.Session{ID: "sess-root", AgentID: "agent-a"}

		raw, err := json.Marshal(api.NewSessionResponse(sess))
		Expect(err).NotTo(HaveOccurred())

		var out map[string]interface{}
		Expect(json.Unmarshal(raw, &out)).To(Succeed())
		Expect(out).NotTo(HaveKey("chainId"),
			"root sessions must stay byte-identical to their pre-field shape")
	})
})
