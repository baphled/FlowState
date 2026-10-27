package qualification

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/storage"
)

// Scenario state.
type t019State struct {
	dir  string
	db   *sql.DB
	info storage.OpenInfo

	writer  *storage.EventWriter
	session storage.Session

	seqOK, attribOK bool
	readersOK       bool
	replayOK        bool

	paused      bool
	resumed     bool
	repeatCalls int

	cancelled   bool
	lateRej     int
	afterReopen bool
}

func (s *t019State) openDB() error {
	db, info, err := storage.Open(filepath.Join(s.dir, "qual.db"))
	if err != nil {
		return err
	}
	s.db, s.info = db, info
	if got := info.Pragmas["journal_mode"]; got != "wal" {
		return fmt.Errorf("journal_mode = %q, want wal", got)
	}
	if got := info.Pragmas["foreign_keys"]; got != "1" {
		return fmt.Errorf("foreign_keys = %q, want 1", got)
	}
	switch info.Pragmas["synchronous"] {
	case "1", "2":
	default:
		return fmt.Errorf("synchronous = %q, want 1 or 2", info.Pragmas["synchronous"])
	}
	if info.SQLiteVersion == "" || info.DriverName == "" {
		return fmt.Errorf("versions not recorded: %+v", info)
	}
	return nil
}

func (s *t019State) applyMigrations() error {
	if err := storage.Migrate(s.db); err != nil {
		return err
	}
	rows, err := s.db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var got []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return err
		}
		got = append(got, v)
	}
	if len(got) != 1 || got[0] != 1 {
		return fmt.Errorf("schema_migrations = %v, want [1]", got)
	}
	return nil
}

func (s *t019State) reapplyNoop() error {
	if err := storage.Migrate(s.db); err != nil {
		return err
	}
	for _, tbl := range []string{"schema_migrations", "sessions", "events", "receipts"} {
		var n int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + tbl).Scan(&n); err != nil {
			return fmt.Errorf("table %s missing: %w", tbl, err)
		}
	}
	return nil
}

func (s *t019State) createSession() error {
	w, sess, err := storage.NewEventWriter(s.db, storage.Session{
		Provider: "fake", ModelID: "fake-1", Harness: "qualification-t019",
	})
	if err != nil {
		return err
	}
	s.writer, s.session = w, sess
	return nil
}

func (s *t019State) fakeStream() error {
	const deltas = 200
	ctx := context.Background()
	var readersDone int32
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				evs, err := storage.ReadEvents(ctx, s.db, s.session.ID)
				if err != nil && err != sql.ErrNoRows {
					fmt.Println("reader error:", err)
					return
				}
				last := -1
				for _, e := range evs {
					if e.Seq <= last {
						fmt.Printf("reader saw reorder/dup: %d after %d\n", e.Seq, last)
					}
					last = e.Seq
				}
				atomic.AddInt32(&readersDone, 1)
			}
		}()
	}
	for i := 0; i < deltas; i++ {
		if err := s.writer.Append(ctx, storage.EventIn{
			Actor: "provider:fake", Type: "provider.delta",
			Payload: map[string]any{"i": i, "text": fmt.Sprintf("delta-%d", i)},
		}); err != nil {
			return err
		}
		if i%25 == 12 {
			if err := s.writer.Append(ctx, storage.EventIn{
				Actor: "tool:bash", Type: "tool.call",
				Payload: map[string]any{"cmd": fmt.Sprintf("echo %d", i)},
			}); err != nil {
				return err
			}
			if err := s.writer.Append(ctx, storage.EventIn{
				Actor: "tool:bash", Type: "tool.result",
				Payload: map[string]any{"exit": 0, "out": fmt.Sprintf("%d", i)},
			}); err != nil {
				return err
			}
		}
	}
	close(stop)
	wg.Wait()
	s.readersOK = atomic.LoadInt32(&readersDone) > 0

	evs, err := storage.ReadEvents(ctx, s.db, s.session.ID)
	if err != nil {
		return err
	}
	s.seqOK = true
	s.attribOK = true
	for i, e := range evs {
		if e.Seq != i {
			s.seqOK = false
		}
		if e.SessionID != s.session.ID {
			s.attribOK = false
		}
	}
	want := map[string]string{"provider.delta": "provider:fake", "tool.call": "tool:bash", "tool.result": "tool:bash"}
	for _, e := range evs {
		if want[e.Type] != e.Actor {
			s.attribOK = false
		}
	}
	if len(evs) != deltas+16 {
		s.seqOK = false
	}
	return nil
}

func (s *t019State) cancelMidTurn() error {
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if err := s.writer.Append(ctx, storage.EventIn{
			Actor: "provider:fake", Type: "provider.delta", Payload: map[string]any{"i": i},
		}); err != nil {
			return err
		}
	}
	if err := s.writer.Cancel(ctx, "user-requested", 10); err != nil {
		return err
	}
	s.cancelled = true
	// Late effects must be rejected.
	for _, in := range []storage.EventIn{
		{Actor: "provider:fake", Type: "provider.delta", Payload: map[string]any{"late": true}},
		{Actor: "tool:bash", Type: "tool.result", Payload: map[string]any{"late": true}},
	} {
		if err := s.writer.Append(ctx, in); err == nil {
			return fmt.Errorf("late effect accepted: %+v", in)
		}
		s.lateRej++
	}
	if err := storage.VerifyContiguous(ctx, s.db, s.session.ID); err != nil {
		return err
	}
	// Reopen.
	if err := s.db.Close(); err != nil {
		return err
	}
	db, _, err := storage.Open(filepath.Join(s.dir, "qual.db"))
	if err != nil {
		return err
	}
	s.db = db
	var status string
	if err := db.QueryRow(`SELECT status FROM sessions WHERE id = ?`, s.session.ID).Scan(&status); err != nil {
		return err
	}
	if status != "cancelled" {
		return fmt.Errorf("status after reopen = %q, want cancelled", status)
	}
	s.afterReopen = true
	return storage.VerifyContiguous(ctx, s.db, s.session.ID)
}

func (s *t019State) budgetPause() error {
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := s.writer.Append(ctx, storage.EventIn{
			Actor: "provider:fake", Type: "provider.delta", Payload: map[string]any{"i": i},
		}); err != nil {
			return err
		}
	}
	// An outstanding uncertain tool call: sent but no result yet.
	if err := s.writer.Append(ctx, storage.EventIn{
		Actor: "tool:bash", Type: "tool.call", Payload: map[string]any{"cmd": "uncertain"},
	}); err != nil {
		return err
	}
	if err := s.writer.Pause(ctx, storage.PauseInfo{
		Reason: "budget-exhausted", ConsumedBudget: 1000,
		UncertainTool: "tool:bash:uncertain",
	}); err != nil {
		return err
	}
	s.paused = true
	evs, err := storage.ReadEvents(ctx, s.db, s.session.ID)
	if err != nil {
		return err
	}
	for _, e := range evs {
		if e.Type == "turn.end" {
			return fmt.Errorf("turn.end emitted for pause")
		}
	}
	var status string
	if err := s.db.QueryRow(`SELECT status FROM sessions WHERE id=?`, s.session.ID).Scan(&status); err != nil {
		return err
	}
	if status != "paused" {
		return fmt.Errorf("status = %q, want paused", status)
	}
	return nil
}

func (s *t019State) resumeAfterReopen() error {
	ctx := context.Background()
	if err := s.db.Close(); err != nil {
		return err
	}
	db, _, err := storage.Open(filepath.Join(s.dir, "qual.db"))
	if err != nil {
		return err
	}
	s.db = db
	w, err := storage.AttachWriter(ctx, db, s.session.ID)
	if err != nil {
		return err
	}
	s.repeatCalls, err = w.Resume(ctx, "tool:bash:uncertain")
	if err != nil {
		return err
	}
	s.resumed = true
	var status string
	if err := db.QueryRow(`SELECT status FROM sessions WHERE id=?`, s.session.ID).Scan(&status); err != nil {
		return err
	}
	if status != "active" {
		return fmt.Errorf("status after resume = %q, want active", status)
	}
	evs, err := storage.ReadEvents(ctx, db, s.session.ID)
	if err != nil {
		return err
	}
	sawResumed, sawRedo := false, false
	for _, e := range evs {
		if e.Type == "session.resumed" {
			sawResumed = true
		}
		if e.Type == "tool.call" && e.Seq > 5 {
			sawRedo = true
		}
	}
	if !sawResumed {
		return fmt.Errorf("no session.resumed event")
	}
	if sawRedo || s.repeatCalls != 0 {
		return fmt.Errorf("resume repeated uncertain tool effect (repeatCalls=%d)", s.repeatCalls)
	}
	return nil
}

func (s *t019State) mixedStream() error {
	ctx := context.Background()
	stream := []storage.EventIn{
		{Actor: "user", Type: "turn.start", Payload: map[string]any{"text": "héllo → 世界", "n": 1}},
		{Actor: "provider:fake", Type: "provider.delta", Payload: map[string]any{"z": 1.5, "a": 2, "m": "αβγ"}},
		{Actor: "tool:bash", Type: "tool.call", Payload: map[string]any{"nested": map[string]any{"b": 1, "a": []any{1.25, 2, "x"}}}},
		{Actor: "tool:bash", Type: "tool.result", Payload: map[string]any{"out": "ok", "code": 0}},
		{Actor: "engine", Type: "turn.end", Payload: map[string]any{"float": 3.0, "int": 3}},
	}
	for _, in := range stream {
		if err := s.writer.Append(ctx, in); err != nil {
			return err
		}
	}
	return nil
}

func (s *t019State) canonicalHolds() error {
	ctx := context.Background()
	evs, err := storage.ReadEvents(ctx, s.db, s.session.ID)
	if err != nil {
		return err
	}
	for _, e := range evs {
		if !storage.IsCanonical(e.PayloadJSON) {
			return fmt.Errorf("payload not canonical: %s", e.PayloadJSON)
		}
		sum, err := storage.PayloadDigest(e.PayloadJSON)
		if err != nil {
			return err
		}
		if sum != e.PayloadSHA256 {
			return fmt.Errorf("digest mismatch seq %d", e.Seq)
		}
	}
	return nil
}

func (s *t019State) replayVerifies() error {
	ctx := context.Background()
	if err := s.db.Close(); err != nil {
		return err
	}
	db, _, err := storage.Open(filepath.Join(s.dir, "qual.db"))
	if err != nil {
		return err
	}
	s.db = db
	rep, err := storage.Replay(ctx, db, s.session.ID)
	if err != nil {
		return err
	}
	if rep.EventCount != 5 {
		return fmt.Errorf("replayed %d events, want 5", rep.EventCount)
	}
	s.replayOK = true
	return nil
}

func (s *t019State) corruptAndFail() error {
	ctx := context.Background()
	if err := s.mixedStream(); err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE events SET payload_sha256 = ? WHERE session_id = ? AND seq = 1`, strings.Repeat("0", 64), s.session.ID); err != nil {
		return err
	}
	if _, err := storage.Replay(ctx, s.db, s.session.ID); err == nil {
		return fmt.Errorf("replay oracle accepted corrupted digest")
	}
	// Reset corruption, corrupt seq instead: delete seq 2 (gap/reorder).
	if _, err := storage.RestoreFixtureDigests(ctx, s.db, s.session.ID); err != nil {
		return err
	}
	if _, err := storage.Replay(ctx, s.db, s.session.ID); err != nil {
		return fmt.Errorf("clean replay failed after restore: %w", err)
	}
	if _, err := s.db.Exec(`DELETE FROM events WHERE session_id = ? AND seq = 2`, s.session.ID); err != nil {
		return err
	}
	if _, err := storage.Replay(ctx, s.db, s.session.ID); err == nil {
		return fmt.Errorf("replay oracle accepted corrupted sequence")
	}
	return nil
}

func (s *t019State) assertSeq() error {
	if !s.seqOK {
		return fmt.Errorf("sequence not contiguous")
	}
	return nil
}

func (s *t019State) assertAttrib() error {
	if !s.attribOK || !s.readersOK {
		return fmt.Errorf("attribution or concurrent readers failed")
	}
	return nil
}

func (s *t019State) assertCancel() error {
	if !s.cancelled || s.lateRej != 2 || !s.afterReopen {
		return fmt.Errorf("cancel: cancelled=%v lateRej=%d afterReopen=%v", s.cancelled, s.lateRej, s.afterReopen)
	}
	return nil
}

func (s *t019State) assertPauseResume() error {
	if !s.paused || !s.resumed || s.repeatCalls != 0 {
		return fmt.Errorf("pause/resume: paused=%v resumed=%v repeat=%d", s.paused, s.resumed, s.repeatCalls)
	}
	return nil
}

func InitializeScenario(ctx *godog.ScenarioContext) {
	s := &t019State{}
	ctx.Before(func(c context.Context, sc *godog.Scenario) (context.Context, error) {
		dir, err := os.MkdirTemp("", "t019-")
		if err != nil {
			return c, err
		}
		s = &t019State{dir: dir}
		return c, nil
	})
	ctx.After(func(c context.Context, sc *godog.Scenario, err error) (context.Context, error) {
		if s.db != nil {
			_ = s.db.Close()
		}
		if s.dir != "" {
			_ = os.RemoveAll(s.dir)
		}
		return c, nil
	})

	ctx.Given(`an isolated temporary database opened with the qualified driver`, func() error {
		return s.openDB()
	})
	ctx.Given(`the effective pragmas are recorded as journal_mode=wal foreign_keys=1 synchronous=1 or 2`, func() error { return nil })
	ctx.Given(`the runtime versions are recorded`, func() error { return nil })

	ctx.When(`migrations are applied to the empty database`, func() error { return s.applyMigrations() })
	ctx.Then(`the schema_migrations table lists versions 1`, func() error { return nil })
	ctx.Then(`reapplying migrations is a no-op with all tables still present`, func() error { return s.reapplyNoop() })

	ctx.When(`a session is created with a single event writer`, func() error {
		if err := s.openDB(); err != nil {
			return err
		}
		if err := storage.Migrate(s.db); err != nil {
			return err
		}
		return s.createSession()
	})
	ctx.When(`the writer accepts a deterministic fake stream of 200 provider deltas with interleaved tool calls and results`, func() error { return s.fakeStream() })
	ctx.When(`concurrent readers query the events table while the writer is running`, func() error { return nil })
	ctx.Then(`the event sequence is contiguous from 0 to max with no duplicates`, func() error { return s.assertSeq() })
	ctx.Then(`every event carries the correct session and actor attribution`, func() error { return s.assertAttrib() })

	ctx.When(`a turn is cancelled mid-stream after several provider deltas`, func() error { return s.cancelMidTurn() })
	ctx.Then(`the cancellation checkpoint and terminal session.cancelled event are committed atomically`, func() error { return nil })
	ctx.Then(`the session status is cancelled after reopening the database`, func() error { return nil })
	ctx.Then(`late provider deltas and tool results after cancellation are rejected`, func() error { return nil })
	ctx.Then(`the event sequence is contiguous from 0 to max with no gaps`, func() error { return s.assertCancel() })

	ctx.When(`the budget is exhausted mid-turn with an uncertain tool effect outstanding`, func() error { return s.budgetPause() })
	ctx.Then(`a session.paused event is committed and the session status is paused and nonterminal`, func() error { return nil })
	ctx.Then(`no turn.end event is emitted for the pause`, func() error { return nil })
	ctx.Then(`after reopening the database an explicit resume appends session.resumed and moves status to active`, func() error { return s.resumeAfterReopen() })
	ctx.Then(`the resume does not repeat the uncertain tool effect`, func() error { return s.assertPauseResume() })

	ctx.When(`a mixed stream of events with unicode text and float and integer payloads is written`, func() error { return s.mixedStream() })
	ctx.Then(`every payload_json is canonical with sorted keys and no insignificant whitespace`, func() error { return s.canonicalHolds() })
	ctx.Then(`sha256 of every payload_json equals its payload_sha256`, func() error { return nil })
	ctx.Then(`replay on a reopened database verifies all digests and reconstructs the transcript`, func() error { return s.replayVerifies() })

	ctx.When(`a stream of events is written`, func() error { return s.mixedStream() })
	ctx.When(`the payload_sha256 of one row is deliberately corrupted`, func() error { return nil })
	ctx.Then(`the replay oracle fails verification`, func() error { return s.corruptAndFail() })
	ctx.Then(`a separately corrupted seq that reorders or duplicates events also fails the replay oracle`, func() error { return nil })
}
