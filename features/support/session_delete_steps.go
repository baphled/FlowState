//go:build e2e

package support

import (
	"context"
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/cucumber/godog"

	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/tui/intents/sessionbrowser"
)

// Phase 16 step glue:
//
// Step definitions for the "Delete a session" BDD scenario (previously
// tagged @wip). The glue drives the real product code:
//
//   - ctxstore.FileSessionStore persists and deletes sessions on disk.
//   - sessionbrowser.Intent handles the UI flow: the 'd' keybinding opens
//     the confirmation modal; 'y'/Enter confirm; anything else cancels.
//
// This mirrors the P9b pattern: in-process fakes where they avoid touching
// the filesystem (none are needed here — the store is cheap enough to
// exercise for real against a temp directory), and real product code
// everywhere else. Behaviour asserted:
//
//   - Pressing 'd' in the browser transitions the intent into
//     `confirmingDelete` (prompt visible) — satisfies
//     "I should be prompted to confirm deletion".
//   - Confirming with 'y' invokes FileSessionStore.Delete and emits a
//     SessionDeletedMsg with a nil Err — satisfies
//     "it should no longer appear in the session list" after a fresh
//     List() call round-trips against disk.

// SessionDeleteSteps holds state for the Delete-a-session BDD scenario.
//
// The struct owns its own temp directory and FileSessionStore so that it
// does not share any state with the generic StepDefinitions fixture. The
// After hook cleans up the temp dir so successive scenarios do not leak
// session files into each other.
type SessionDeleteSteps struct {
	tempDir            string
	store              *ctxstore.FileSessionStore
	browser            *sessionbrowser.Intent
	confirmPromptSeen  bool
	deletedMsg         *sessionbrowser.SessionDeletedMsg
	targetSessionID    string
	targetSessionTitle string
}

// RegisterSessionDeleteSteps registers the delete-session BDD steps against
// the supplied scenario context. A fresh state struct is created per call
// so scenarios cannot contaminate one another.
//
// Expected:
//   - sc is a non-nil godog ScenarioContext for step registration.
//
// Side effects:
//   - Registers Before/After hooks and step definitions on sc.
func RegisterSessionDeleteSteps(sc *godog.ScenarioContext) {
	s := &SessionDeleteSteps{}

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

	sc.Step(`^I have a session named "([^"]*)"$`, s.iHaveASessionNamed)
	sc.Step(`^I delete "([^"]*)"$`, s.iDelete)
	sc.Step(`^it should no longer appear in the session list$`, s.itShouldNoLongerAppearInTheSessionList)
	sc.Step(`^I should be prompted to confirm deletion$`, s.iShouldBePromptedToConfirmDeletion)
}

// reset clears transient state between scenarios.
//
// Side effects:
//   - Zeroes every field on s without touching the (already-cleaned) temp
//     dir.
func (s *SessionDeleteSteps) reset() {
	s.store = nil
	s.browser = nil
	s.confirmPromptSeen = false
	s.deletedMsg = nil
	s.targetSessionID = ""
	s.targetSessionTitle = ""
}

// iHaveASessionNamed creates a FileSessionStore backed by a temp directory
// and persists a single session whose SessionInfo.Title equals `name`. A
// second, unrelated "Other Session" is also persisted so the scenario
// exercises deletion against a *list* rather than an edge-case one-item
// store.
//
// Expected:
//   - name is a non-empty, human-readable session title.
//
// Returns:
//   - An error if the temp dir or store cannot be created, or if the
//     session cannot be persisted.
//
// Side effects:
//   - Creates a temp dir at $TMPDIR/flowstate-session-delete-*.
//   - Writes two JSON session files into that dir.
func (s *SessionDeleteSteps) iHaveASessionNamed(name string) error {
	tmpDir, err := os.MkdirTemp("", "flowstate-session-delete-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	s.tempDir = tmpDir

	store, err := ctxstore.NewFileSessionStore(tmpDir)
	if err != nil {
		return fmt.Errorf("creating session store: %w", err)
	}
	s.store = store

	sessionID := "to-delete-session-001"
	ctxStore := recall.NewEmptyContextStore("test-model")
	if err := store.Save(sessionID, ctxStore, ctxstore.SessionMetadata{Title: name}); err != nil {
		return fmt.Errorf("saving target session: %w", err)
	}
	s.targetSessionID = sessionID
	s.targetSessionTitle = name

	// Persist a second, unrelated session so the list has more than one
	// entry. Without this the adjustSelectionAfterDelete branch that
	// snaps the cursor up one row is exercised trivially; with it we get
	// the more realistic two-item case.
	otherCtx := recall.NewEmptyContextStore("test-model")
	if err := store.Save("other-session-001", otherCtx, ctxstore.SessionMetadata{Title: "Other Session"}); err != nil {
		return fmt.Errorf("saving secondary session: %w", err)
	}

	return nil
}

// iDelete drives the sessionbrowser.Intent through the full delete flow:
// construct the intent from the store's current list, navigate to the
// target row, press 'd' to open the confirmation modal, assert the modal
// is visible, then press 'y' to confirm. The SessionDeletedMsg command is
// resolved synchronously (command functions are pure closures returning a
// tea.Msg) so the resulting message is captured for later assertions.
//
// Expected:
//   - iHaveASessionNamed has already populated s.store and
//     s.targetSessionTitle with a matching session.
//   - name matches s.targetSessionTitle (the scenario references the same
//     session throughout).
//
// Returns:
//   - An error if the store/browser are not initialised, if the target
//     row cannot be found, if 'd' does not open the confirmation modal,
//     or if the confirmation step does not produce a SessionDeletedMsg.
//
// Side effects:
//   - Mutates s.browser, s.confirmPromptSeen, s.deletedMsg.
//   - Performs a real filesystem delete against s.tempDir.
func (s *SessionDeleteSteps) iDelete(name string) error {
	if s.store == nil {
		return errors.New("no session store available — did Given step run?")
	}
	if name != s.targetSessionTitle {
		return fmt.Errorf("scenario asked to delete %q but the Given named %q", name, s.targetSessionTitle)
	}

	entries, selectedIdx, err := s.buildEntries()
	if err != nil {
		return err
	}

	s.browser = sessionbrowser.NewIntent(sessionbrowser.IntentConfig{
		Sessions: entries,
		Deleter:  s.store,
	})
	// The "New Session" row is at index 0, so session N lives at index N+1.
	for range selectedIdx + 1 {
		s.browser.Update(tea.KeyMsg{Type: tea.KeyDown})
	}

	// Press 'd' → confirmation modal opens.
	s.browser.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if !s.browser.IsConfirmingDelete() {
		return errors.New("expected browser to be in confirming-delete state after pressing 'd'")
	}
	s.confirmPromptSeen = true

	// Press 'y' → deletion is performed, SessionDeletedMsg is returned
	// via the command closure.
	cmd := s.browser.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		return errors.New("expected a tea.Cmd from the confirm keypress, got nil")
	}
	msg := cmd()
	deleted, ok := msg.(sessionbrowser.SessionDeletedMsg)
	if !ok {
		return fmt.Errorf("expected SessionDeletedMsg from confirm command, got %T", msg)
	}
	s.deletedMsg = &deleted
	if deleted.Err != nil {
		return fmt.Errorf("delete command reported an error: %w", deleted.Err)
	}

	return nil
}

// itShouldNoLongerAppearInTheSessionList re-reads the session list from
// disk (via FileSessionStore.List) and confirms that the session whose
// title matched `targetSessionTitle` is gone, while the unrelated
// secondary session remains.
//
// Returns:
//   - An error if the store is missing, the target session still exists,
//     or the secondary session was collaterally deleted.
//
// Side effects:
//   - Reads the session directory from disk.
func (s *SessionDeleteSteps) itShouldNoLongerAppearInTheSessionList() error {
	if s.store == nil {
		return errors.New("no session store available — did Given step run?")
	}
	remaining := s.store.List()
	for i := range remaining {
		if remaining[i].Title == s.targetSessionTitle {
			return fmt.Errorf("expected session titled %q to be gone, still present with ID %q", s.targetSessionTitle, remaining[i].ID)
		}
	}
	// Confirm the secondary session survived — guards against an
	// accidental "delete everything" regression.
	foundOther := false
	for i := range remaining {
		if remaining[i].Title == "Other Session" {
			foundOther = true
			break
		}
	}
	if !foundOther {
		return errors.New("expected the unrelated 'Other Session' to survive the delete")
	}
	return nil
}

// iShouldBePromptedToConfirmDeletion checks that the confirmation modal
// was actually opened during iDelete. The flag is set the moment the
// intent reports IsConfirmingDelete() after the 'd' keypress, so this is
// a genuine assertion on the UI flow rather than a bookkeeping check.
//
// Returns:
//   - An error if the modal was never observed.
//
// Side effects:
//   - None.
func (s *SessionDeleteSteps) iShouldBePromptedToConfirmDeletion() error {
	if !s.confirmPromptSeen {
		return errors.New("expected the delete confirmation prompt to have been shown")
	}
	return nil
}

// buildEntries translates the store's SessionInfo list into sessionbrowser
// SessionEntry values and locates the index of the target session.
//
// Returns:
//   - The converted entry slice.
//   - The index (into that slice) of the session whose title matches
//     s.targetSessionTitle.
//   - An error if the target session is not present in the list.
//
// Side effects:
//   - None.
func (s *SessionDeleteSteps) buildEntries() ([]sessionbrowser.SessionEntry, int, error) {
	infos := s.store.List()
	entries := make([]sessionbrowser.SessionEntry, 0, len(infos))
	targetIdx := -1
	for i := range infos {
		if infos[i].Title == s.targetSessionTitle {
			targetIdx = i
		}
		entries = append(entries, sessionbrowser.SessionEntry{
			ID:           infos[i].ID,
			Title:        infos[i].Title,
			MessageCount: infos[i].MessageCount,
			LastActive:   infos[i].LastActive,
		})
	}
	if targetIdx < 0 {
		return nil, -1, fmt.Errorf("target session %q not found in store listing (size=%d)", s.targetSessionTitle, len(infos))
	}
	return entries, targetIdx, nil
}
