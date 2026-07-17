package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/session"
)

// errorOnListStore is a coordination.Store fake whose List call always
// returns the configured error. Used by the closeSessionIfManaged spec
// that verifies a store failure produces a warning without blocking the
// session close.
type errorOnListStore struct {
	err error
}

func (s *errorOnListStore) Get(string) ([]byte, error)    { return nil, coordination.ErrKeyNotFound }
func (s *errorOnListStore) Set(string, []byte) error      { return nil }
func (s *errorOnListStore) List(string) ([]string, error) { return nil, s.err }
func (s *errorOnListStore) Delete(string) error           { return nil }
func (s *errorOnListStore) Increment(string) (int, error) { return 0, nil }
func (s *errorOnListStore) Exists(string) (bool, error)   { return false, nil }

var _ = Describe("closeSessionIfManaged deliverable check", func() {
	var (
		buf        *bytes.Buffer
		origLogger *slog.Logger
	)

	BeforeEach(func() {
		buf = &bytes.Buffer{}
		origLogger = slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	})

	AfterEach(func() {
		slog.SetDefault(origLogger)
	})

	It("does not warn when a delegated session wrote coordination_store keys", func() {
		mgr := session.NewManager(nil)
		mgr.RegisterSession("parent-1", "orchestrator")
		child, err := mgr.CreateWithParentAndChain("parent-1", "health-researcher", "chain-abc")
		Expect(err).NotTo(HaveOccurred())

		store := coordination.NewMemoryStore()
		Expect(store.Set("chain-abc/result", []byte("findings"))).To(Succeed())

		d := &DelegateTool{sessionManager: mgr, coordinationStore: store}
		d.closeSessionIfManaged(child.ID)

		Expect(buf.String()).NotTo(ContainSubstring("coordination_store"))
		sess, getErr := mgr.GetSession(child.ID)
		Expect(getErr).NotTo(HaveOccurred())
		Expect(sess.Status).To(Equal("completed"))
	})

	It("warns when a delegated session wrote zero coordination_store keys", func() {
		mgr := session.NewManager(nil)
		mgr.RegisterSession("parent-1", "orchestrator")
		child, err := mgr.CreateWithParentAndChain("parent-1", "health-researcher", "chain-abc")
		Expect(err).NotTo(HaveOccurred())

		store := coordination.NewMemoryStore()

		d := &DelegateTool{sessionManager: mgr, coordinationStore: store}
		d.closeSessionIfManaged(child.ID)

		output := buf.String()
		Expect(output).To(ContainSubstring("without writing any coordination_store keys"))
		Expect(output).To(ContainSubstring(child.ID))
		Expect(output).To(ContainSubstring("health-researcher"))
		Expect(output).To(ContainSubstring("chain-abc"))
	})

	It("warns when a delegated session wrote under the chain prefix but missed the exact required coordination_store key", func() {
		mgr := session.NewManager(nil)
		mgr.RegisterSession("parent-1", "orchestrator")
		child, err := mgr.CreateWithParentAndChain("parent-1", "Writer", "chain-abc")
		Expect(err).NotTo(HaveOccurred())
		child.Messages = append(child.Messages, session.Message{
			Role:    "user",
			Content: "chainID=chain-abc. Write your result to coordination_store key=**chain-abc/final-output**.",
		})

		store := coordination.NewMemoryStore()
		Expect(store.Set("chain-abc/other-output", []byte("wrong key"))).To(Succeed())

		d := &DelegateTool{sessionManager: mgr, coordinationStore: store}
		d.closeSessionIfManaged(child.ID)

		output := buf.String()
		Expect(output).To(ContainSubstring("without writing its required coordination_store key"))
		Expect(output).To(ContainSubstring("chain-abc/final-output"))
		Expect(output).NotTo(ContainSubstring("without writing any coordination_store keys"))
	})

	It("logs the engine fallback warning instead of the generic missing-key warning when a fallback envelope exists", func() {
		mgr := session.NewManager(nil)
		mgr.RegisterSession("parent-1", "orchestrator")
		child, err := mgr.CreateWithParentAndChain("parent-1", "explorer", "chain-abc")
		Expect(err).NotTo(HaveOccurred())
		child.Messages = append(child.Messages, session.Message{
			Role:    "user",
			Content: "chainID=chain-abc. Write your result to coordination_store key=chain-abc/codebase-findings.",
		})

		store := coordination.NewMemoryStore()
		payload, err := json.Marshal(deliveryFailureEnvelope{
			Status:         "delivery_failed_engine_fallback",
			SessionID:      child.ID,
			AgentID:        "explorer",
			ChainID:        "chain-abc",
			FailureSummary: "all providers failed",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(store.Set("chain-abc/_engine_fallback/explorer/delivery_failure", payload)).To(Succeed())

		d := &DelegateTool{sessionManager: mgr, coordinationStore: store}
		d.closeSessionIfManaged(child.ID)

		output := buf.String()
		Expect(output).To(ContainSubstring("engine-persisted delivery failure fallback"))
		Expect(output).To(ContainSubstring("all providers failed"))
		Expect(output).NotTo(ContainSubstring("without writing its required coordination_store key"))
	})

	It("consumes a session-scoped fallback envelope when the chain-scoped key is absent", func() {
		mgr := session.NewManager(nil)
		mgr.RegisterSession("parent-1", "orchestrator")
		child, err := mgr.CreateWithParentAndChain("parent-1", "explorer", "chain-abc")
		Expect(err).NotTo(HaveOccurred())
		child.Messages = append(child.Messages, session.Message{
			Role:    "user",
			Content: "chainID=chain-abc. Write your result to coordination_store key=chain-abc/codebase-findings.",
		})

		store := coordination.NewMemoryStore()
		payload, err := json.Marshal(deliveryFailureEnvelope{
			Status:         "delivery_failed_engine_fallback",
			SessionID:      child.ID,
			AgentID:        "explorer",
			ChainID:        "chain-abc",
			FailureSummary: "all providers failed",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(store.Set(child.ID+"/_engine_fallback/explorer/delivery_failure", payload)).To(Succeed())

		d := &DelegateTool{sessionManager: mgr, coordinationStore: store}
		d.closeSessionIfManaged(child.ID)

		output := buf.String()
		Expect(output).To(ContainSubstring("engine-persisted delivery failure fallback"))
		Expect(output).To(ContainSubstring(child.ID + "/_engine_fallback/explorer/delivery_failure"))
		Expect(output).NotTo(ContainSubstring("without writing its required coordination_store key"))
	})

	It("prefers the real coordination key over a fallback envelope", func() {
		mgr := session.NewManager(nil)
		mgr.RegisterSession("parent-1", "orchestrator")
		child, err := mgr.CreateWithParentAndChain("parent-1", "explorer", "chain-abc")
		Expect(err).NotTo(HaveOccurred())
		child.Messages = append(child.Messages, session.Message{
			Role:    "user",
			Content: "chainID=chain-abc. Write your result to coordination_store key=chain-abc/codebase-findings.",
		})

		store := coordination.NewMemoryStore()
		Expect(store.Set("chain-abc/codebase-findings", []byte("real result"))).To(Succeed())
		payload, err := json.Marshal(deliveryFailureEnvelope{
			Status:         "delivery_failed_engine_fallback",
			SessionID:      child.ID,
			AgentID:        "explorer",
			ChainID:        "chain-abc",
			FailureSummary: "all providers failed",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(store.Set("chain-abc/_engine_fallback/explorer/delivery_failure", payload)).To(Succeed())

		d := &DelegateTool{sessionManager: mgr, coordinationStore: store}
		d.closeSessionIfManaged(child.ID)

		output := buf.String()
		Expect(output).NotTo(ContainSubstring("engine-persisted delivery failure fallback"))
		Expect(output).NotTo(ContainSubstring("without writing its required coordination_store key"))
	})

	It("does not warn for a non-delegated session without a parent_id", func() {
		mgr := session.NewManager(nil)
		top, err := mgr.CreateSession("orchestrator")
		Expect(err).NotTo(HaveOccurred())

		store := coordination.NewMemoryStore()

		d := &DelegateTool{sessionManager: mgr, coordinationStore: store}
		d.closeSessionIfManaged(top.ID)

		Expect(buf.String()).NotTo(ContainSubstring("coordination_store"))
	})

	It("does not warn when coordinationStore is nil", func() {
		mgr := session.NewManager(nil)
		mgr.RegisterSession("parent-1", "orchestrator")
		child, err := mgr.CreateWithParentAndChain("parent-1", "health-researcher", "chain-abc")
		Expect(err).NotTo(HaveOccurred())

		d := &DelegateTool{sessionManager: mgr, coordinationStore: nil}
		d.closeSessionIfManaged(child.ID)

		Expect(buf.String()).NotTo(ContainSubstring("coordination_store"))
	})

	It("warns about a List error but still closes the session", func() {
		mgr := session.NewManager(nil)
		mgr.RegisterSession("parent-1", "orchestrator")
		child, err := mgr.CreateWithParentAndChain("parent-1", "health-researcher", "chain-abc")
		Expect(err).NotTo(HaveOccurred())

		store := &errorOnListStore{err: errors.New("store unavailable")}

		d := &DelegateTool{sessionManager: mgr, coordinationStore: store}
		d.closeSessionIfManaged(child.ID)

		output := buf.String()
		Expect(output).To(ContainSubstring("coordination_store check failed"))
		Expect(output).To(ContainSubstring("store unavailable"))
		sess, getErr := mgr.GetSession(child.ID)
		Expect(getErr).NotTo(HaveOccurred())
		Expect(sess.Status).To(Equal("completed"))
	})
})
