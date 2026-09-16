package session_test

import (
	"os"
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
			sess, err := mgr.CreateSessionWithDefaults("debounce-agent", "prov", "model")
			Expect(err).NotTo(HaveOccurred())

			createCall := waitForPersists(persists, 1, 2*time.Second)
			Expect(createCall).To(HaveLen(1), "session creation persists synchronously")

			for i := 0; i < 3; i++ {
				mgr.AppendSessionMessageForTest(sess.ID, session.Message{
					ID:      debounceMsgID(i),
					Role:    "user",
					Content: "under the mutation limit so only the time sweeper can flush",
				})
			}

			sweepCalls := waitForPersists(persists, 1, 2*time.Second)
			Expect(sweepCalls).To(HaveLen(1),
				"the sweeper must flush a dirty session within the debounce interval")
			Expect(sweepCalls[0].msgN).To(BeNumerically(">=", 3),
				"the sweep write carries the buffered appends")
		})

		It("coalesces a burst of appends into a single sweep write", func() {
			Expect(mgr.SetPersistDebounceForTest(50 * time.Millisecond)).To(Succeed())
			sess, err := mgr.CreateSessionWithDefaults("coalesce-agent", "prov", "model")
			Expect(err).NotTo(HaveOccurred())
			waitForPersists(persists, 1, 2*time.Second)

			for i := 0; i < session.PersistMutationLimit-1; i++ {
				mgr.AppendSessionMessageForTest(sess.ID, session.Message{
					ID:      debounceMsgID(i),
					Role:    "user",
					Content: "bursty turn coalesced by the debounce sweeper",
				})
			}

			sweepCalls := waitForPersists(persists, 1, 2*time.Second)
			Expect(sweepCalls).To(HaveLen(1),
				"a burst under the mutation limit must land as one sidecar write")
		})
	})

	Describe("Stop semantics", func() {
		It("performs a final flush on Stop and exits the sweeper goroutine", func() {
			Expect(mgr.SetPersistDebounceForTest(10 * time.Second)).To(Succeed())
			sess, err := mgr.CreateSessionWithDefaults("stop-agent", "prov", "model")
			Expect(err).NotTo(HaveOccurred())
			waitForPersists(persists, 1, 2*time.Second)

			mgr.AppendSessionMessageForTest(sess.ID, session.Message{
				ID:      "pre-stop-1",
				Role:    "user",
				Content: "dirty at shutdown; Stop must flush it",
			})

			Expect(mgr.Stop()).To(Succeed())
			finalCalls := waitForPersists(persists, 1, 2*time.Second)
			Expect(finalCalls).To(HaveLen(1), "Stop flushes the dirty session on the way down")
		})

		It("does not start a new sweeper after Stop when a session is marked dirty again", func() {
			Expect(mgr.SetPersistDebounceForTest(30 * time.Millisecond)).To(Succeed())
			sess, err := mgr.CreateSessionWithDefaults("post-stop-agent", "prov", "model")
			Expect(err).NotTo(HaveOccurred())
			waitForPersists(persists, 1, 2*time.Second)

			Expect(mgr.Stop()).To(Succeed())
			drainPersists(persists)

			mgr.AppendSessionMessageForTest(sess.ID, session.Message{
				ID:      "post-stop-1",
				Role:    "user",
				Content: "must not lazily restart a sweeper after Stop",
			})

			Consistently(persists, 300*time.Millisecond, 50*time.Millisecond).ShouldNot(Receive(),
				"a post-Stop dirty-mark must not restart the sweeper goroutine")
		})
	})

	Describe("tool-anomaly softened append persistence regression (d96da3bd)", func() {
		It("marks the session dirty when a softened tool-anomaly message is appended", func() {
			Expect(mgr.SetPersistDebounceForTest(30 * time.Millisecond)).To(Succeed())
			sess, err := mgr.CreateSessionWithDefaults("anomaly-agent", "prov", "model")
			Expect(err).NotTo(HaveOccurred())
			waitForPersists(persists, 1, 2*time.Second)

			mgr.AppendSessionMessageForTest(sess.ID, session.Message{
				ID:      "anomaly-1",
				Role:    "assistant",
				Status:  "anomaly",
				Content: "softened tool-anomaly append must reach the sidecar",
			})

			sweepCalls := waitForPersists(persists, 1, 2*time.Second)
			Expect(sweepCalls).To(HaveLen(1),
				"the anomaly-fix dirty-mark path must flush via the sweeper")
		})
	})
})

// debounceMsgID generates a deterministic message ID for debounce tests.
func debounceMsgID(i int) string {
	return "debounce-msg-" + time.Now().Format("150405") + "-" + string(rune('a'+i))
}

// drainPersists empties any buffered persist calls without blocking.
func drainPersists(persists chan persistCall) {
	for {
		select {
		case <-persists:
		default:
			return
		}
	}
}
