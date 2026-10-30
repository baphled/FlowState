package store

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Session update clock", func() {
	var sess *Session
	var before, after time.Time

	BeforeEach(func() {
		s := newTestStore()
		ctx := context.Background()
		_, err := s.CreateSession(ctx, "clock-session", "provider", "model", "harness", "config")
		Expect(err).NotTo(HaveOccurred())
		before = time.Now().UTC()
		err = s.UpdateSessionStatus(ctx, "clock-session", "completed")
		after = time.Now().UTC()
		Expect(err).NotTo(HaveOccurred())
		sess, err = s.GetSession(ctx, "clock-session")
		Expect(err).NotTo(HaveOccurred())
	})

	It("records an update no earlier than the call began", func() {
		Expect(sess.UpdatedAt).To(BeTemporally(">=", before))
	})

	It("records an update no later than the call returned", func() {
		Expect(sess.UpdatedAt).To(BeTemporally("<=", after))
	})

	It("keeps the update at or after creation", func() {
		Expect(sess.UpdatedAt).To(BeTemporally(">=", sess.CreatedAt))
	})
})

var _ = Describe("Session creation time ties", func() {
	It("orders equal creation timestamps by session ID", func() {
		s := newTestStore()
		ctx := context.Background()
		for _, id := range []string{"z-session", "a-session"} {
			_, err := s.CreateSession(ctx, id, "provider", "model", "harness", "config")
			Expect(err).NotTo(HaveOccurred())
		}
		_, err := s.db.ExecContext(ctx, `UPDATE sessions SET created_at_utc=?`, "2026-01-01T00:00:00Z")
		Expect(err).NotTo(HaveOccurred())
		sessions, err := s.ListSessions(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(sessions).To(WithTransform(func(rows []Session) []string {
			ids := make([]string, len(rows))
			for i, row := range rows {
				ids[i] = row.ID
			}
			return ids
		}, Equal([]string{"a-session", "z-session"})))
	})
})
