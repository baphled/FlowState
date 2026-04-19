package support

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/cucumber/godog"

	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// Phase 18b step glue:
//
// Step definitions for the "Fork a session" BDD scenario, previously
// tagged @wip. The glue drives the real FileSessionStore.Fork API —
// there is no TUI round-trip here because the scenario is framed at the
// API level ("I fork the session at message 3"). The sessionbrowser
// intent's 'f' affordance is covered exhaustively by the package-level
// ginkgo tests, which obviates the need for duplicate BDD assertions on
// the TUI flow.
//
// Behaviour asserted:
//
//   - A new session is produced whose ID differs from the origin.
//   - That new session carries messages 1..pivot (inclusive), matching
//     the scenario's "it should contain messages 1 through 3".
//   - The origin session's on-disk state is unchanged — the fork is a
//     one-way copy, matching "the original session should be unchanged".

// SessionForkSteps owns the temp dir, store, origin session ID, and
// fork outcome for the P18b scenario. Each Before hook resets state so
// scenarios cannot contaminate one another.
//
// A pointer to the main StepDefinitions is retained so the fork flow
// can broadcast the new session ID into the shared fixture's session
// fields — that way the cross-scenario "a new session should be
// created" step (registered in steps.go and also used by the @smoke
// startup scenario) continues to validate against a single source of
// truth.
type SessionForkSteps struct {
	shared         *StepDefinitions
	tempDir        string
	store          *ctxstore.FileSessionStore
	originID       string
	originMessages []string
	originPivotID  string
	forkedID       string
}

// RegisterSessionForkSteps wires the "Fork a session" BDD glue onto the
// supplied scenario context. A fresh state struct is built per call so
// Before/After hooks do not leak filesystem artefacts.
//
// Expected:
//   - sc is a non-nil godog ScenarioContext.
//   - shared is the main StepDefinitions so the fork flow can publish
//     its outcome into fields consumed by cross-scenario steps.
//
// Side effects:
//   - Registers Before/After hooks and step definitions on sc.
func RegisterSessionForkSteps(sc *godog.ScenarioContext, shared *StepDefinitions) {
	s := &SessionForkSteps{shared: shared}

	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})

	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if s.tempDir != "" {
			_ = os.RemoveAll(s.tempDir)
			s.tempDir = ""
		}
		return ctx, nil
	})

	sc.Step(`^I am in a session with history$`, s.iAmInASessionWithHistory)
	sc.Step(`^I fork the session at message (\d+)$`, s.iForkTheSessionAtMessage)
	sc.Step(`^it should contain messages (\d+) through (\d+)$`, s.itShouldContainMessagesThrough)
	sc.Step(`^the original session should be unchanged$`, s.theOriginalSessionShouldBeUnchanged)
	// The generic "a new session should be created" step lives in
	// steps.go — shared with the @smoke startup scenario. Fork
	// publishes its outcome into the shared StepDefinitions so that
	// single step continues to validate against a single source of
	// truth regardless of which flow created the session.
}

// reset zeroes transient state between scenarios. The temp dir is
// cleaned by the After hook separately so the reset never races with
// concurrent disk activity.
//
// Side effects:
//   - Clears every SessionForkSteps field except tempDir (which the
//     After hook owns).
func (s *SessionForkSteps) reset() {
	s.store = nil
	s.originID = ""
	s.originMessages = nil
	s.originPivotID = ""
	s.forkedID = ""
}

// iAmInASessionWithHistory creates a FileSessionStore backed by a temp
// dir and persists an origin session with five messages alternating
// user/assistant. Five is the chosen size because the scenario uses
// "message 3" as the pivot, so a five-message origin leaves both sides
// of the pivot non-empty.
//
// Returns:
//   - An error if the temp dir, store, or save step fails.
//
// Side effects:
//   - Creates a temp dir at $TMPDIR/flowstate-session-fork-*.
//   - Writes an origin session JSON file into that directory.
func (s *SessionForkSteps) iAmInASessionWithHistory() error {
	tmpDir, err := os.MkdirTemp("", "flowstate-session-fork-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	s.tempDir = tmpDir

	store, err := ctxstore.NewFileSessionStore(tmpDir)
	if err != nil {
		return fmt.Errorf("creating session store: %w", err)
	}
	s.store = store

	s.originID = "origin-session-001"
	ctxStore := recall.NewEmptyContextStore("test-model")
	contents := []string{"m1", "m2", "m3", "m4", "m5"}
	for i, c := range contents {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		ctxStore.Append(provider.Message{Role: role, Content: c})
	}
	if err := store.Save(s.originID, ctxStore, ctxstore.SessionMetadata{Title: "Origin Session"}); err != nil {
		return fmt.Errorf("saving origin session: %w", err)
	}
	s.originMessages = contents
	return nil
}

// iForkTheSessionAtMessage translates the 1-based "message N" from the
// scenario into the N-th StoredMessage.ID on disk, then invokes
// FileSessionStore.Fork with that ID.
//
// Expected:
//   - iAmInASessionWithHistory has already populated s.store and
//     s.originID.
//   - messageNumber is 1-based and within the origin's history length.
//
// Returns:
//   - An error when the store is missing, the message number is out of
//     range, or the Fork call itself fails.
//
// Side effects:
//   - Performs filesystem I/O (read origin, write fork).
func (s *SessionForkSteps) iForkTheSessionAtMessage(messageNumber int) error {
	if s.store == nil {
		return errors.New("no session store available — did Given step run?")
	}
	if messageNumber < 1 {
		return fmt.Errorf("message number must be 1-based, got %d", messageNumber)
	}

	originStore, err := s.store.Load(s.originID)
	if err != nil {
		return fmt.Errorf("loading origin: %w", err)
	}
	stored := originStore.GetStoredMessages()
	idx := messageNumber - 1
	if idx >= len(stored) {
		return fmt.Errorf("message %d out of range (origin has %d messages)", messageNumber, len(stored))
	}
	s.originPivotID = stored[idx].ID

	newID, err := s.store.Fork(s.originID, s.originPivotID)
	if err != nil {
		return fmt.Errorf("fork call: %w", err)
	}
	s.forkedID = newID

	// Publish the fork outcome to the shared StepDefinitions so the
	// generic "a new session should be created" step (registered in
	// steps.go and co-owned by the @smoke startup scenario) validates
	// against the fork's real session ID. This keeps cross-scenario
	// assertions consistent without reintroducing a duplicate step
	// implementation here.
	if s.shared != nil {
		s.shared.currentSessionID = newID
		s.shared.session = &TestSession{id: newID}
	}
	return nil
}

// itShouldContainMessagesThrough loads the fork and asserts its message
// history matches messages start..end (both 1-based, inclusive) of the
// origin. The scenario reads "messages 1 through 3" so start=1, end=3
// and the fork must hold exactly three messages whose content matches
// the origin's m1..m3.
//
// Expected:
//   - iForkTheSessionAtMessage has already populated s.forkedID.
//   - start is 1 (the scenario's range always begins at message 1) and
//     end >= start; callers outside the BDD flow should respect those
//     same bounds.
//
// Returns:
//   - An error if the fork cannot be loaded, the count is wrong, or any
//     message content fails to match the origin.
//
// Side effects:
//   - Reads the fork session file.
func (s *SessionForkSteps) itShouldContainMessagesThrough(start, end int) error {
	if start != 1 {
		return fmt.Errorf("step expected messages to start at 1; scenario asked for %d", start)
	}
	if end < start {
		return fmt.Errorf("invalid range %d..%d", start, end)
	}

	forkStore, err := s.store.Load(s.forkedID)
	if err != nil {
		return fmt.Errorf("loading fork: %w", err)
	}
	forkMsgs := forkStore.AllMessages()
	expectedCount := end - start + 1
	if len(forkMsgs) != expectedCount {
		return fmt.Errorf("fork contains %d messages, want %d", len(forkMsgs), expectedCount)
	}
	for i, msg := range forkMsgs {
		if msg.Content != s.originMessages[i] {
			return fmt.Errorf("fork message %d content %q, want %q", i+1, msg.Content, s.originMessages[i])
		}
	}
	return nil
}

// theOriginalSessionShouldBeUnchanged reloads the origin after the fork
// has been written and verifies its message count and content are
// identical to the state set up in iAmInASessionWithHistory. This is
// the guard against a regression where Fork accidentally mutates the
// origin (the classic pitfall when slice headers are shared).
//
// Returns:
//   - An error if the origin cannot be reloaded, has the wrong message
//     count, or any message content differs from the original seed.
//
// Side effects:
//   - Reads the origin session file.
func (s *SessionForkSteps) theOriginalSessionShouldBeUnchanged() error {
	originStore, err := s.store.Load(s.originID)
	if err != nil {
		return fmt.Errorf("reloading origin: %w", err)
	}
	got := originStore.AllMessages()
	if len(got) != len(s.originMessages) {
		return fmt.Errorf("origin now has %d messages, want %d", len(got), len(s.originMessages))
	}
	for i, msg := range got {
		if msg.Content != s.originMessages[i] {
			return fmt.Errorf("origin message %d content %q, want %q", i+1, msg.Content, s.originMessages[i])
		}
	}
	return nil
}
