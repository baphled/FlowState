package chat_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
	"github.com/baphled/flowstate/internal/tui/uikit/navigation"
)

// stubSessionManager implements chat.SessionManager for testing the session
// trail ancestry walk. Only GetSession carries meaningful logic; the other
// methods are no-ops satisfying the interface contract.
type stubSessionManager struct {
	sessions map[string]*session.Session
}

func (s *stubSessionManager) EnsureSession(_ string, _ string) {}

func (s *stubSessionManager) SendMessage(
	_ context.Context, _ string, _ string,
) (<-chan provider.StreamChunk, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *stubSessionManager) GetSession(id string) (*session.Session, error) {
	sess, ok := s.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %q not found", id)
	}
	return sess, nil
}

func (s *stubSessionManager) UpdateSessionAgent(_ string, _ string) error {
	return nil
}

var _ = Describe("Session trail wiring", func() {
	var intent *chat.Intent

	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "root-session",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
			TokenBudget:  4096,
		})
	})

	AfterEach(func() {
		chat.SetRunningInTestsForTest(false)
	})

	It("initialises with an empty trail when sessionManager is nil", func() {
		trail := chat.SessionTrailForTest(intent)
		Expect(trail).NotTo(BeNil())
		Expect(trail.Items()).To(BeEmpty())
	})

	It("does not panic when refreshSessionTrail is called with nil sessionManager", func() {
		Expect(func() {
			chat.RefreshSessionTrailForTest(intent)
		}).NotTo(Panic())
	})

	Context("with a single root session", func() {
		BeforeEach(func() {
			mgr := &stubSessionManager{
				sessions: map[string]*session.Session{
					"root-session": {
						ID:      "root-session",
						AgentID: "orchestrator",
					},
				},
			}
			intent.SetSessionManagerForTest(mgr)
			chat.RefreshSessionTrailForTest(intent)
		})

		It("produces a single-item trail", func() {
			items := chat.SessionTrailForTest(intent).Items()
			Expect(items).To(HaveLen(1))
			Expect(items[0]).To(Equal(navigation.SessionTrailItem{
				SessionID: "root-session",
				AgentID:   "orchestrator",
				Label:     "orchestrator",
			}))
		})
	})

	Context("with a three-level ancestry chain", func() {
		BeforeEach(func() {
			mgr := &stubSessionManager{
				sessions: map[string]*session.Session{
					"grandparent": {
						ID:      "grandparent",
						AgentID: "planner",
					},
					"parent": {
						ID:       "parent",
						AgentID:  "tech-lead",
						ParentID: "grandparent",
					},
					"child": {
						ID:       "child",
						AgentID:  "engineer",
						ParentID: "parent",
					},
				},
			}
			intent.SetSessionManagerForTest(mgr)
			intent.SetSessionIDForTest("child")
			chat.RefreshSessionTrailForTest(intent)
		})

		It("produces three items in root-first order", func() {
			items := chat.SessionTrailForTest(intent).Items()
			Expect(items).To(HaveLen(3))
			Expect(items[0].SessionID).To(Equal("grandparent"))
			Expect(items[0].AgentID).To(Equal("planner"))
			Expect(items[1].SessionID).To(Equal("parent"))
			Expect(items[1].AgentID).To(Equal("tech-lead"))
			Expect(items[2].SessionID).To(Equal("child"))
			Expect(items[2].AgentID).To(Equal("engineer"))
		})
	})

	Context("with a broken chain (missing parent)", func() {
		BeforeEach(func() {
			mgr := &stubSessionManager{
				sessions: map[string]*session.Session{
					"child": {
						ID:       "child",
						AgentID:  "engineer",
						ParentID: "missing-parent",
					},
				},
			}
			intent.SetSessionManagerForTest(mgr)
			intent.SetSessionIDForTest("child")
			chat.RefreshSessionTrailForTest(intent)
		})

		It("terminates cleanly with only the reachable session", func() {
			items := chat.SessionTrailForTest(intent).Items()
			Expect(items).To(HaveLen(1))
			Expect(items[0].SessionID).To(Equal("child"))
		})
	})

	Context("with a circular parent chain", func() {
		BeforeEach(func() {
			mgr := &stubSessionManager{
				sessions: map[string]*session.Session{
					"alpha": {
						ID:       "alpha",
						AgentID:  "agent-a",
						ParentID: "beta",
					},
					"beta": {
						ID:       "beta",
						AgentID:  "agent-b",
						ParentID: "alpha",
					},
				},
			}
			intent.SetSessionManagerForTest(mgr)
			intent.SetSessionIDForTest("alpha")
			chat.RefreshSessionTrailForTest(intent)
		})

		It("breaks the loop via visited-set and does not hang", func() {
			items := chat.SessionTrailForTest(intent).Items()
			Expect(items).To(HaveLen(2))
			// Root-first: beta (parent) then alpha (current).
			Expect(items[0].SessionID).To(Equal("beta"))
			Expect(items[1].SessionID).To(Equal("alpha"))
		})
	})
})
