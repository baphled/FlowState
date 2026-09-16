package session_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/session"
)

// persistCall timestamps one persistFn invocation so the debounce
// tests can assert when the sidecar write landed without wall-clock
// sleeps against the production 5s interval.
type persistCall struct {
	at    time.Time
	msgN  int
	agent string
}

func waitForPersists(calls chan persistCall, want int, timeout time.Duration) []persistCall {
	var got []persistCall
	deadline := time.After(timeout)
	for len(got) < want {
		select {
		case c := <-calls:
			got = append(got, c)
		case <-deadline:
			return got
		}
	}
	return got
}

var _ = Describe("Persist debounce flusher", func() {
	var (
		sessionsDir string
		mgr         *session.Manager
		persists    chan persistCall
	)

	BeforeEach(func() {
		var err error
		sessionsDir, err = os.MkdirTemp("", "persist-debounce-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { os.RemoveAll(sessionsDir) })

		persists = make(chan persistCall, 64)
		mgr = session.NewManager(nil)
		mgr.SetSessionsDir(sessionsDir)
		mgr.SetPersistFnForTest(func(dir string, s *session.Session) error {
			persists <- persistCall{at: time.Now(), msgN: len(s.Messages), agent: s.AgentID}
			return session.PersistSession(dir, s)
		})
	})

	Describe("time-driven flush within PersistDebounceInterval", func() {
		It("flushes a session with fewer than PersistMutationLimit appends without an explicit flush call", func() {
			Expect(mgr.SetPersistDebounceForTest(30 * time.Millisecond)).To(Succeed())
			sess, err := mgr.CreateSession("debounce-agent")
			Expect(err).NotTo(HaveOccurred())

			createCall := waitForPersists(persists, 1, 2*time.Second)
			Expect(createCall).To(HaveLen(1), "session creation persists synchronously")

			for i := 0; i < 3; i++ {
				mgr.AppendMessage(sess.ID, session.Message{
					ID:      timeDrivenMsgID(i),
					Role:    "user",
					Content: "under the mutation limit so only the time sweeper can flush",
				})
			}

			flushed := waitForPersists(persists, 1, 2*time.Second)
			Expect(flushed).To(HaveLen(1),
				"the debounce flusher must write the sidecar within PersistDebounceInterval of the first mutation")
			Expect(flushed[0].msgN).To(Equal(3), "the flush must include all buffered appends")
		})

		It("stops the flusher on Stop so a shut-down manager persists nothing further", func() {
			Expect(mgr.SetPersistDebounceForTest(20 * time.Millisecond)).To(Succeed())
			sess, err := mgr.CreateSession("stop-agent")
			Expect(err).NotTo(HaveOccurred())
			_ = waitForPersists(persists, 1, 2*time.Second)

			Expect(mgr.Stop()).To(Succeed())

			mgr.AppendMessage(sess.ID, session.Message{
				ID:      "post-stop",
				Role:    "user",
				Content: "appended after Stop; must never reach disk via the sweeper",
			})
			Consistently(persists, "150ms").ShouldNot(Receive(),
				"no further debounce flush may fire after Stop")
		})
	})

	Describe("tool-anomaly softened append dirty-mark gap", func() {
		It("marks the sidecar dirty on a tolerated tool-anomaly append so Stop flushes the buffered message", func() {
			sess, err := mgr.CreateSession("anomaly-agent")
			Expect(err).NotTo(HaveOccurred())
			_ = waitForPersists(persists, 1, 2*time.Second)

			mgr.AppendMessage(sess.ID, session.Message{
				ID:         "anomaly-1",
				Role:       "assistant",
				Content:    "announced a tool call in thinking but emitted none",
				StopReason: session.StopReasonToolUseNoCalls,
			})

			Expect(mgr.Stop()).To(Succeed())

			raw, err := os.ReadFile(filepath.Join(sessionsDir, sess.ID+".meta.json"))
			Expect(err).NotTo(HaveOccurred())
			var sidecar struct {
				Messages []session.Message `json:"messages"`
			}
			Expect(json.Unmarshal(raw, &sidecar)).To(Succeed())
			Expect(sidecar.Messages).To(HaveLen(1),
				"the tool-anomaly early-return path must markPersistDirtyLocked so the buffered append survives shutdown")
		})
	})
})

func timeDrivenMsgID(i int) string {
	return "debounce-msg-" + string(rune('a'+i))
}
