//go:build e2e

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

// SessionIsolationSteps holds state for session isolation and tool call visibility BDD scenarios.
type SessionIsolationSteps struct {
	sessionStore    *ctxstore.FileSessionStore
	newContextStore *recall.FileContextStore
	loadedStore     *recall.FileContextStore
	savedSessionID  string
	tempDir         string
}

// RegisterSessionIsolationSteps registers step definitions for session isolation and tool call visibility.
//
// Expected:
//   - sc is a valid godog ScenarioContext for step registration.
//   - s is a non-nil SessionIsolationSteps instance shared across related registration functions.
//
// Side effects:
//   - Registers Before hooks, After hooks, and step definitions on the provided scenario context.
func RegisterSessionIsolationSteps(sc *godog.ScenarioContext, s *SessionIsolationSteps) {
	sc.Before(func(bctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.sessionStore = nil
		s.newContextStore = nil
		s.loadedStore = nil
		s.savedSessionID = ""
		return bctx, nil
	})

	sc.After(func(bctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if s.tempDir != "" {
			os.RemoveAll(s.tempDir)
			s.tempDir = ""
		}
		return bctx, nil
	})

	sc.Step(`^I have a session store with existing messages$`, s.iHaveASessionStoreWithExistingMessages)
	sc.Step(`^I create a new empty context store$`, s.iCreateANewEmptyContextStore)
	sc.Step(`^the new session should have no messages$`, s.theNewSessionShouldHaveNoMessages)

	sc.Step(`^a session was saved with tool call messages$`, s.aSessionWasSavedWithToolCallMessages)
	sc.Step(`^I load the session$`, s.iLoadTheSession)
	sc.Step(`^the loaded session should contain the tool call message$`, s.theLoadedSessionShouldContainTheToolCallMessage)
}

// iHaveASessionStoreWithExistingMessages creates a session store with a saved session containing messages.
//
// Expected:
//   - No prior state is required.
//
// Returns:
//   - An error if the temp directory, session store, or context store cannot be created.
//
// Side effects:
//   - Creates a temporary directory with a session store containing two messages.
func (s *SessionIsolationSteps) iHaveASessionStoreWithExistingMessages() error {
	tmpDir, err := os.MkdirTemp("", "flowstate-session-isolation-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	s.tempDir = tmpDir

	sessionStore, err := ctxstore.NewFileSessionStore(tmpDir)
	if err != nil {
		return fmt.Errorf("creating session store: %w", err)
	}
	s.sessionStore = sessionStore

	store := recall.NewEmptyContextStore("test-model")
	store.Append(provider.Message{Role: "user", Content: "Hello from old session"})
	store.Append(provider.Message{Role: "assistant", Content: "Hi there!"})

	if err := sessionStore.Save("old-session", store, ctxstore.SessionMetadata{}); err != nil {
		return fmt.Errorf("saving old session: %w", err)
	}

	return nil
}

// iCreateANewEmptyContextStore creates a fresh empty context store simulating new session creation.
//
// Expected:
//   - iHaveASessionStoreWithExistingMessages has been called.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Sets s.newContextStore to a fresh empty context store.
func (s *SessionIsolationSteps) iCreateANewEmptyContextStore() error {
	s.newContextStore = recall.NewEmptyContextStore("")
	return nil
}

// theNewSessionShouldHaveNoMessages asserts the new context store has zero messages.
//
// Expected:
//   - iCreateANewSession has been called.
//
// Returns:
//   - An error if the new context store is nil or contains messages.
//
// Side effects:
//   - None.
func (s *SessionIsolationSteps) theNewSessionShouldHaveNoMessages() error {
	if s.newContextStore == nil {
		return errors.New("new context store is nil")
	}
	messages := s.newContextStore.GetStoredMessages()
	if len(messages) != 0 {
		return fmt.Errorf("expected 0 messages, got %d", len(messages))
	}
	return nil
}

// saveSessionWithMessages creates a temporary directory, session store, and persists the given messages.
//
// Expected:
//   - dirPrefix is a non-empty string for the temporary directory name pattern.
//   - sessionID is the identifier under which to save the session.
//   - messages is a non-empty slice of messages to persist.
//
// Returns:
//   - An error if directory creation, store creation, or save fails.
//
// Side effects:
//   - Sets tempDir, sessionStore, and savedSessionID on the receiver.
func (s *SessionIsolationSteps) saveSessionWithMessages(dirPrefix, sessionID string, messages []provider.Message) error {
	tmpDir, err := os.MkdirTemp("", dirPrefix)
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	s.tempDir = tmpDir

	sessionStore, err := ctxstore.NewFileSessionStore(tmpDir)
	if err != nil {
		return fmt.Errorf("creating session store: %w", err)
	}
	s.sessionStore = sessionStore

	store := recall.NewEmptyContextStore("test-model")
	for _, msg := range messages {
		store.Append(msg)
	}

	s.savedSessionID = sessionID
	if err := sessionStore.Save(s.savedSessionID, store, ctxstore.SessionMetadata{}); err != nil {
		return fmt.Errorf("saving session %q: %w", sessionID, err)
	}

	return nil
}

// aSessionWasSavedWithToolCallMessages creates a session with a user message and an assistant tool call message.
//
// Expected:
//   - No prior state is required.
//
// Returns:
//   - An error if the temp directory, session store, or context store cannot be created.
//
// Side effects:
//   - Creates a temporary directory with a session store containing a tool call message.
func (s *SessionIsolationSteps) aSessionWasSavedWithToolCallMessages() error {
	return s.saveSessionWithMessages("flowstate-tool-calls-*", "tool-session", []provider.Message{
		{Role: "user", Content: "Run a command"},
		{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{Name: "bash"}}},
	})
}

// iLoadTheSession loads the saved session from the session store using the savedSessionID.
//
// Expected:
//   - A Given step has populated savedSessionID and sessionStore.
//
// Returns:
//   - An error if loading the session fails.
//
// Side effects:
//   - Sets s.loadedStore to the loaded context store.
func (s *SessionIsolationSteps) iLoadTheSession() error {
	loaded, err := s.sessionStore.Load(s.savedSessionID)
	if err != nil {
		return fmt.Errorf("loading session %q: %w", s.savedSessionID, err)
	}
	s.loadedStore = loaded
	return nil
}

// theLoadedSessionShouldContainTheToolCallMessage asserts the loaded session has a tool call message.
//
// Expected:
//   - iLoadTheSession has been called.
//
// Returns:
//   - An error if the loaded store is nil or does not contain an assistant message with tool calls.
//
// Side effects:
//   - None.
func (s *SessionIsolationSteps) theLoadedSessionShouldContainTheToolCallMessage() error {
	if s.loadedStore == nil {
		return errors.New("loaded store is nil")
	}
	messages := s.loadedStore.GetStoredMessages()
	for _, sm := range messages {
		if sm.Message.Role == "assistant" && len(sm.Message.ToolCalls) > 0 {
			return nil
		}
	}
	return fmt.Errorf("expected an assistant message with tool calls, found %d messages without one", len(messages))
}
