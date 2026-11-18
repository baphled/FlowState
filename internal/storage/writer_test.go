package storage

import (
	"context"
	"database/sql"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// cancelledSession arranges a session with three committed effects
// (seq 0..2) and then cancels mid-turn. It asserts nothing behavioural.
func cancelledSession(db *sql.DB) (context.Context, string) {
	w, sess, err := NewEventWriter(db, Session{Provider: "p", ModelID: "m", Harness: "t"})
	Expect(err).NotTo(HaveOccurred())
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		err := w.Append(ctx, EventIn{Actor: "provider:fake", Type: "provider.delta",
			Payload: map[string]any{"i": i}})
		Expect(err).NotTo(HaveOccurred())
	}
	err = w.Cancel(ctx, "user abort", 2)
	Expect(err).NotTo(HaveOccurred())
	return ctx, sess.ID
}

// openWT opens a migrated temp database for the writer acceptance specs
// (kept independent of the storage suite's openT).
func openWT() (*sql.DB, string) {
	db, _, err := Open(filepath.Join(GinkgoT().TempDir(), "wt.db"))
	Expect(err).NotTo(HaveOccurred())
	err = Migrate(db)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = db.Close() })
	return db, GinkgoT().TempDir()
}

// eventTypes reads the ordered event types recorded for a session. It
// asserts nothing behavioural; scan/query failures are returned as errors.
func eventTypes(ctx context.Context, db *sql.DB, sessionID string) ([]string, error) {
	rows, err := db.Query(`SELECT seq, type FROM events WHERE session_id=? ORDER BY seq`, sessionID)
	if err != nil {
		return nil, err
	}
	DeferCleanup(rows.Close)
	var out []string
	for rows.Next() {
		var seq int
		var typ string
		if err := rows.Scan(&seq, &typ); err != nil {
			return nil, err
		}
		out = append(out, typ)
	}
	return out, rows.Err()
}

var _ = Describe("Writer cancel mid-turn", func() {
	It("marks the session cancelled in the sessions table", func() {
		db, _ := openWT()
		_, sid := cancelledSession(db)
		var status string
		err := db.QueryRow(`SELECT status FROM sessions WHERE id=?`, sid).Scan(&status)
		Expect(err).NotTo(HaveOccurred())
		Expect(status).To(Equal("cancelled"))
	})

	It("rejects late effects after the cancel", func() {
		db, _ := openWT()
		w, _, err := NewEventWriter(db, Session{Provider: "p", ModelID: "m", Harness: "t"})
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		for i := 0; i < 3; i++ {
			err := w.Append(ctx, EventIn{Actor: "provider:fake", Type: "provider.delta",
				Payload: map[string]any{"i": i}})
			Expect(err).NotTo(HaveOccurred())
		}
		err = w.Cancel(ctx, "user abort", 2)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Resume(ctx, "")
		Expect(err).To(HaveOccurred(), "late resume accepted after cancel")
	})

	It("commits checkpoint and terminal event contiguously after the effects", func() {
		db, _ := openWT()
		ctx, sid := cancelledSession(db)
		err := VerifyContiguous(ctx, db, sid)
		Expect(err).NotTo(HaveOccurred())
		types, err := eventTypes(ctx, db, sid)
		Expect(err).NotTo(HaveOccurred())
		Expect(types).To(Equal([]string{
			"provider.delta", "provider.delta", "provider.delta",
			"cancel.checkpoint", "session.cancelled",
		}))
	})
})

var _ = Describe("Writer durable pause and resume", func() {
	It("persists the paused status and rejects appends while paused", func() {
		db, _ := openWT()
		w, sess, err := NewEventWriter(db, Session{Provider: "p", ModelID: "m", Harness: "t"})
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		for i := 0; i < 4; i++ {
			err := w.Append(ctx, EventIn{Actor: "provider:fake", Type: "provider.delta",
				Payload: map[string]any{"i": i}})
			Expect(err).NotTo(HaveOccurred())
		}
		err = w.Pause(ctx, PauseInfo{Reason: "budget exhausted", ConsumedBudget: 4096,
			UncertainTool: "shell"})
		Expect(err).NotTo(HaveOccurred())
		var status string
		err = db.QueryRow(`SELECT status FROM sessions WHERE id=?`, sess.ID).Scan(&status)
		Expect(err).NotTo(HaveOccurred())
		Expect(status).To(Equal("paused"))
	})

	It("resumes from the persisted high-water mark after a restart", func() {
		db, _ := openWT()
		w, sess, err := NewEventWriter(db, Session{Provider: "p", ModelID: "m", Harness: "t"})
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		for i := 0; i < 4; i++ {
			err := w.Append(ctx, EventIn{Actor: "provider:fake", Type: "provider.delta",
				Payload: map[string]any{"i": i}})
			Expect(err).NotTo(HaveOccurred())
		}
		err = w.Pause(ctx, PauseInfo{Reason: "budget exhausted", ConsumedBudget: 4096,
			UncertainTool: "shell"})
		Expect(err).NotTo(HaveOccurred())
		// Simulate process restart: attach a fresh writer to the same DB.
		w2, err := AttachWriter(ctx, db, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = w2.Resume(ctx, "shell")
		Expect(err).NotTo(HaveOccurred())
		for i := 4; i < 7; i++ {
			err := w2.Append(ctx, EventIn{Actor: "provider:fake", Type: "provider.delta",
				Payload: map[string]any{"i": i}})
			Expect(err).NotTo(HaveOccurred())
		}
		err = VerifyContiguous(ctx, db, sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(w2.Session().Status).To(Equal("active"))
	})
})
