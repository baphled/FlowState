package store

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Adversarial pair: both inside the same millisecond; the trimmed string of the
// EARLIER instant sorts AFTER the later one as text ('Z' > '0').
const (
	trimmedEarlier = "2026-11-22T02:03:27.131Z"
	wideLater      = "2026-11-22T02:03:27.131052Z"
)

// fixedLayoutPattern matches the fixed layout: nine fractional digits and Z.
const fixedLayoutPattern = `^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{9}Z$`

// mustRawExec runs a seeding statement; failures are setup panics.
func mustRawExec(s *Store, query string, args ...any) {
	if _, err := s.db.Exec(query, args...); err != nil {
		panic(err)
	}
}

var _ = Describe("Stored timestamp format", func() {
	It("writes exactly nine fractional digits for a whole-millisecond instant", func() {
		Expect(iso(time.Date(2026, 11, 22, 2, 3, 27, 131000000, time.UTC))).To(Equal("2026-11-22T02:03:27.131000000Z"))
	})

	It("writes nine fractional digits for an instant with no fraction", func() {
		Expect(iso(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))).To(MatchRegexp(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{9}Z$`))
	})

	It("writes a fixed-width string for a session whose instant ends in a zero digit", func() {
		s := newTestStore()
		// Draw sessions until the clock yields an instant whose nanoseconds end in
		// zero, the case time.RFC3339Nano would trim; about one draw in ten.
		var trimmedID string
		for i := 0; i < 5000 && trimmedID == ""; i++ {
			id := fmt.Sprintf("fmt-%d", i)
			sess, err := s.CreateSession(context.Background(), id, "p", "m", "h", "c")
			if err != nil {
				panic(err)
			}
			if sess.CreatedAt.Nanosecond()%10 == 0 {
				trimmedID = id
			}
		}
		if trimmedID == "" {
			panic("clock never produced an instant ending in a zero digit")
		}
		var created string
		if err := s.db.QueryRow(`SELECT created_at_utc FROM sessions WHERE id=?`, trimmedID).Scan(&created); err != nil {
			panic(err)
		}
		Expect(created).To(MatchRegexp(fixedLayoutPattern))
	})

	It("parses an old trimmed string", func() {
		parsed, err := time.Parse(time.RFC3339Nano, trimmedEarlier)
		Expect(err).NotTo(HaveOccurred())
		Expect(parsed.Nanosecond()).To(Equal(131000000))
	})

	It("parses a new fixed-width string to the same instant", func() {
		old, _ := time.Parse(time.RFC3339Nano, trimmedEarlier)
		wide, err := time.Parse(time.RFC3339Nano, "2026-11-22T02:03:27.131000000Z")
		Expect(err).NotTo(HaveOccurred())
		Expect(wide.Equal(old)).To(BeTrue())
	})
})

// seedChat creates a root session and a child that the root may steer.
func seedChat(s *Store) {
	ctx := context.Background()
	if _, err := s.CreateSession(ctx, "root", "p", "m", "h", "c"); err != nil {
		panic(err)
	}
	if _, err := s.CreateChildSession(ctx, "chat", "root", "p", "m", "h", "c"); err != nil {
		panic(err)
	}
}

var _ = Describe("Chronological reads over old variable-width rows", func() {
	var s *Store
	var ctx context.Context

	BeforeEach(func() {
		s = newTestStore()
		ctx = context.Background()
	})

	It("lists the earlier of an adversarial session pair first", func() {
		for _, id := range []string{"a-later", "z-earlier"} {
			if _, err := s.CreateSession(ctx, id, "p", "m", "h", "c"); err != nil {
				panic(err)
			}
		}
		mustRawExec(s, `UPDATE sessions SET created_at_utc=? WHERE id='a-later'`, wideLater)
		mustRawExec(s, `UPDATE sessions SET created_at_utc=? WHERE id='z-earlier'`, trimmedEarlier)
		got, err := s.ListSessions(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(got[0].ID).To(Equal("z-earlier"))
	})

	It("breaks ties by id when equal instants are stored in different widths", func() {
		for _, id := range []string{"a-id", "z-id"} {
			if _, err := s.CreateSession(ctx, id, "p", "m", "h", "c"); err != nil {
				panic(err)
			}
		}
		mustRawExec(s, `UPDATE sessions SET created_at_utc=? WHERE id='a-id'`, trimmedEarlier)
		mustRawExec(s, `UPDATE sessions SET created_at_utc=? WHERE id='z-id'`, "2026-11-22T02:03:27.131000000Z")
		got, err := s.ListSessions(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(got[0].ID).To(Equal("a-id"))
	})

	It("delivers the earlier of an adversarial pending message pair first", func() {
		seedChat(s)
		first, err := s.SendMessage(ctx, "chat", "root", "first")
		if err != nil {
			panic(err)
		}
		second, err := s.SendMessage(ctx, "chat", "root", "second")
		if err != nil {
			panic(err)
		}
		mustRawExec(s, `UPDATE session_messages SET created_at_utc=? WHERE id=?`, wideLater, first.ID)
		mustRawExec(s, `UPDATE session_messages SET created_at_utc=? WHERE id=?`, trimmedEarlier, second.ID)
		got, err := s.PendingSteeringMessages(ctx, "chat")
		Expect(err).NotTo(HaveOccurred())
		Expect(got[0].ID).To(Equal(second.ID))
	})

	It("breaks equal message timestamps by id when insertion order disagrees", func() {
		seedChat(s)
		a, _ := s.SendMessage(ctx, "chat", "root", "a")
		b, _ := s.SendMessage(ctx, "chat", "root", "b")
		// Same instant, mixed widths, so the SQL text order cannot supply the
		// tiebreak: the lower-id row carries the trimmed text, which sorts
		// after the fixed-width text of the higher-id row.
		mustRawExec(s, `UPDATE session_messages SET created_at_utc=? WHERE id=?`, trimmedEarlier, a.ID)
		mustRawExec(s, `UPDATE session_messages SET created_at_utc=? WHERE id=?`, "2026-11-22T02:03:27.131000000Z", b.ID)
		got, err := s.PendingSteeringMessages(ctx, "chat")
		Expect(err).NotTo(HaveOccurred())
		Expect(got[0].ID).To(Equal(a.ID))
	})

	It("lists the earlier of an adversarial swarm run pair first", func() {
		first := recordSwarmRun(s, sampleSwarmRun("tsorder", "pass"))
		second := recordSwarmRun(s, sampleSwarmRun("tsorder", "pass"))
		mustRawExec(s, `UPDATE swarm_runs SET created_at_utc=? WHERE id=?`, wideLater, first.ID)
		mustRawExec(s, `UPDATE swarm_runs SET created_at_utc=? WHERE id=?`, trimmedEarlier, second.ID)
		got, err := s.SwarmRunsFor(ctx, "tsorder")
		Expect(err).NotTo(HaveOccurred())
		Expect(got[0].ID).To(Equal(second.ID))
	})

	It("breaks equal swarm run timestamps by id when the stored text disagrees", func() {
		first := recordSwarmRun(s, sampleSwarmRun("tsorder", "pass"))
		second := recordSwarmRun(s, sampleSwarmRun("tsorder", "pass"))
		mustRawExec(s, `UPDATE swarm_runs SET created_at_utc=? WHERE id=?`, trimmedEarlier, first.ID)
		mustRawExec(s, `UPDATE swarm_runs SET created_at_utc=? WHERE id=?`, "2026-11-22T02:03:27.131000000Z", second.ID)
		got, err := s.SwarmRunsFor(ctx, "tsorder")
		Expect(err).NotTo(HaveOccurred())
		Expect(got[0].ID).To(Equal(first.ID))
	})
})

var _ = Describe("Unparseable stored timestamps", func() {
	var s *Store
	var ctx context.Context

	BeforeEach(func() {
		s = newTestStore()
		ctx = context.Background()
		seedChat(s)
	})

	// seedGarbageMessages stores two messages and corrupts the first one's timestamp.
	seedGarbageMessages := func() (string, string) {
		bad, err := s.SendMessage(ctx, "chat", "root", "bad")
		if err != nil {
			panic(err)
		}
		good, err := s.SendMessage(ctx, "chat", "root", "good")
		if err != nil {
			panic(err)
		}
		mustRawExec(s, `UPDATE session_messages SET created_at_utc='garbage' WHERE id=?`, bad.ID)
		return bad.ID, good.ID
	}

	It("fails PendingSteeringMessages naming the row", func() {
		bad, _ := seedGarbageMessages()
		_, err := s.PendingSteeringMessages(ctx, "chat")
		Expect(err).To(MatchError(ContainSubstring("row " + bad + ": parse created_at_utc")))
	})

	It("fails the drain with the same error", func() {
		bad, _ := seedGarbageMessages()
		_, err := s.DrainSteeringMessagesFor(ctx, "chat")
		Expect(err).To(MatchError(ContainSubstring("row " + bad + ": parse created_at_utc")))
	})

	It("leaves every message undelivered after a failed drain", func() {
		seedGarbageMessages()
		_, _ = s.DrainSteeringMessagesFor(ctx, "chat")
		var undelivered int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM session_messages WHERE delivered_at_utc IS NULL`).Scan(&undelivered); err != nil {
			panic(err)
		}
		Expect(undelivered).To(Equal(2))
	})

	It("fails SwarmRunsFor naming the row", func() {
		run := recordSwarmRun(s, sampleSwarmRun("tsorder", "pass"))
		mustRawExec(s, `UPDATE swarm_runs SET created_at_utc='garbage' WHERE id=?`, run.ID)
		_, err := s.SwarmRunsFor(ctx, "tsorder")
		Expect(err).To(MatchError(ContainSubstring("row " + run.ID + ": parse created_at_utc")))
	})
})
