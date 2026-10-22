package context_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/streaming"
)

// P18b scope decision: first-cut Fork copies the complete events WAL rather
// than filtering by pivot-message timestamp. StoredMessage has no intrinsic
// timestamp today, so a precise message → WAL-entry join would require a
// schema change that is out of scope for this phase. Full-clone is the
// approved approximation per the task brief's scope cliff.

// SessionFork (P18b) exercises the fork-a-session API on FileSessionStore.
//
// The fork feature lets a user clone a session at a chosen pivot message so
// later writes to the fork do not affect the origin. The tests in this file
// pin down the behaviours that the implementation MUST preserve:
//
//  1. Messages are copied up to and including the pivot.
//  2. Writes to the fork do not mutate the origin's on-disk state.
//  3. An empty pivot ID produces a full clone (last message inclusive).
//  4. Parent-session and pivot-message metadata are persisted on the fork.
//  5. The per-session `.events.jsonl` WAL is copied filtered by timestamp so
//     activity events visible before the pivot are preserved.
var _ = Describe("SessionFork (P18b)", func() {
	var (
		tmpDir       string
		sessionStore *ctxstore.FileSessionStore
	)

	BeforeEach(func() {
		tmpDir = GinkgoT().TempDir()
		var err error
		sessionStore, err = ctxstore.NewFileSessionStore(tmpDir)
		Expect(err).NotTo(HaveOccurred())
	})

	// seedSession stores n user/assistant message pairs in a session so each
	// message has a stable StoredMessage.ID. The returned slice holds the
	// StoredMessage entries in save-order for tests that need to pick a
	// pivot by index.
	seedSession := func(id string, n int) []recall.StoredMessage {
		store := recall.NewEmptyContextStore("test-model")
		for i := range n {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			store.Append(provider.Message{Role: role, Content: "msg-" + id + "-" + roleIdx(i)})
		}
		Expect(sessionStore.Save(id, store, ctxstore.SessionMetadata{Title: "Origin " + id})).To(Succeed())
		return store.GetStoredMessages()
	}

	Describe("Fork_CopiesMessagesUpToPivot", func() {
		It("copies messages 1..pivot from origin into the new session", func() {
			stored := seedSession("origin-copy", 5)
			pivot := stored[2] // index 2 → "message 3"

			newID, err := sessionStore.Fork("origin-copy", pivot.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(newID).NotTo(BeEmpty())
			Expect(newID).NotTo(Equal("origin-copy"))

			forkStore, err := sessionStore.Load(newID)
			Expect(err).NotTo(HaveOccurred())
			Expect(forkStore.Count()).To(Equal(3))

			msgs := forkStore.AllMessages()
			Expect(msgs[0].Content).To(Equal(stored[0].Message.Content))
			Expect(msgs[2].Content).To(Equal(stored[2].Message.Content))
		})
	})

	Describe("Fork_WritesToForkDoNotAffectOrigin", func() {
		It("isolates subsequent writes from the origin session", func() {
			stored := seedSession("origin-isolate", 3)
			newID, err := sessionStore.Fork("origin-isolate", stored[1].ID)
			Expect(err).NotTo(HaveOccurred())

			forkStore, err := sessionStore.Load(newID)
			Expect(err).NotTo(HaveOccurred())
			forkStore.Append(provider.Message{Role: "user", Content: "fork-only"})
			Expect(sessionStore.Save(newID, forkStore, ctxstore.SessionMetadata{})).To(Succeed())

			originStore, err := sessionStore.Load("origin-isolate")
			Expect(err).NotTo(HaveOccurred())
			Expect(originStore.Count()).To(Equal(3))
			for _, m := range originStore.AllMessages() {
				Expect(m.Content).NotTo(Equal("fork-only"))
			}
		})
	})

	Describe("Fork_FullCloneWhenPivotEmpty", func() {
		It("clones the entire message history when pivot is empty", func() {
			stored := seedSession("origin-full", 4)

			newID, err := sessionStore.Fork("origin-full", "")
			Expect(err).NotTo(HaveOccurred())

			forkStore, err := sessionStore.Load(newID)
			Expect(err).NotTo(HaveOccurred())
			Expect(forkStore.Count()).To(Equal(len(stored)))
		})
	})

	Describe("Fork_SetsParentAndPivotFields", func() {
		It("writes parent_session_id and pivot_message_id on the fork", func() {
			stored := seedSession("origin-meta", 2)
			newID, err := sessionStore.Fork("origin-meta", stored[0].ID)
			Expect(err).NotTo(HaveOccurred())

			// Inspect the on-disk fork file directly so we pin the
			// persisted shape (parent_session_id + pivot_message_id +
			// forked_at) rather than rely solely on a round-trip.
			raw, err := os.ReadFile(filepath.Join(tmpDir, newID+".json"))
			Expect(err).NotTo(HaveOccurred())

			var meta map[string]interface{}
			Expect(json.Unmarshal(raw, &meta)).To(Succeed())
			Expect(meta["parent_session_id"]).To(Equal("origin-meta"))
			Expect(meta["pivot_message_id"]).To(Equal(stored[0].ID))
			Expect(meta["forked_at"]).NotTo(BeNil())
		})
	})

	Describe("Fork_CopiesEventsJSONL", func() {
		It("copies the full events WAL and leaves the origin's WAL untouched", func() {
			stored := seedSession("origin-events", 3)
			pivot := stored[1]

			now := time.Now().UTC()
			for i := range 3 {
				ev := streaming.SwarmEvent{
					ID:            "evt-" + roleIdx(i),
					Type:          streaming.EventPlan,
					Status:        "completed",
					Timestamp:     now.Add(time.Duration(i) * time.Second),
					AgentID:       "planner",
					SchemaVersion: streaming.CurrentSchemaVersion,
				}
				Expect(sessionStore.AppendEvent("origin-events", ev)).To(Succeed())
			}

			newID, err := sessionStore.Fork("origin-events", pivot.ID)
			Expect(err).NotTo(HaveOccurred())

			forkEvents, err := sessionStore.LoadEvents(newID)
			Expect(err).NotTo(HaveOccurred())
			Expect(forkEvents).To(HaveLen(3))
			forkIDs := []string{forkEvents[0].ID, forkEvents[1].ID, forkEvents[2].ID}
			Expect(forkIDs).To(Equal([]string{"evt-0", "evt-1", "evt-2"}))

			// Mutate fork's WAL and verify the origin's is untouched —
			// the two files must be independent on disk.
			Expect(sessionStore.AppendEvent(newID, streaming.SwarmEvent{
				ID:            "evt-fork-only",
				Type:          streaming.EventPlan,
				Status:        "completed",
				Timestamp:     now.Add(10 * time.Second),
				AgentID:       "planner",
				SchemaVersion: streaming.CurrentSchemaVersion,
			})).To(Succeed())

			originEvents, err := sessionStore.LoadEvents("origin-events")
			Expect(err).NotTo(HaveOccurred())
			Expect(originEvents).To(HaveLen(3), "origin events WAL should be untouched by writes to the fork")
		})

		It("tolerates an origin session with no events WAL", func() {
			stored := seedSession("origin-noevents", 2)
			newID, err := sessionStore.Fork("origin-noevents", stored[0].ID)
			Expect(err).NotTo(HaveOccurred())

			events, err := sessionStore.LoadEvents(newID)
			Expect(err).NotTo(HaveOccurred())
			Expect(events).To(BeEmpty())
		})
	})

	Describe("Fork_ReturnsErrorForMissingOrigin", func() {
		It("returns an error when the origin session does not exist", func() {
			_, err := sessionStore.Fork("does-not-exist", "")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("Fork_ReturnsErrorForUnknownPivot", func() {
		It("returns an error when the pivot message ID is not in the origin", func() {
			_ = seedSession("origin-badpivot", 2)
			_, err := sessionStore.Fork("origin-badpivot", "nope-not-here")
			Expect(err).To(HaveOccurred())
		})
	})
})

// roleIdx formats a small integer as a zero-padded decimal string so
// message content is stable and easy to read in failure messages.
func roleIdx(i int) string {
	const digits = "0123456789"
	if i >= 0 && i < len(digits) {
		return string(digits[i])
	}
	return "X"
}
