package support

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cucumber/godog"

	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/recall"
)

// SessionEnrichmentSteps holds state for session enrichment BDD scenarios.
type SessionEnrichmentSteps struct {
	steps                *StepDefinitions
	sessionStore         *ctxstore.FileSessionStore
	reloadedContextStore *recall.FileContextStore
	legacySessionID      string
	tempDir              string
	loadError            error
	sessionInfo          []ctxstore.SessionInfo
}

// RegisterSessionEnrichmentSteps registers all session enrichment step definitions.
//
// Expected:
//   - sc is a valid godog ScenarioContext for step registration.
//   - steps is a pointer to the shared StepDefinitions instance.
//
// Side effects:
//   - Registers After hooks and step definitions on the provided scenario context.
func RegisterSessionEnrichmentSteps(sc *godog.ScenarioContext, steps *StepDefinitions) {
	s := &SessionEnrichmentSteps{steps: steps}

	sc.After(func(bddCtx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if s.tempDir != "" {
			os.RemoveAll(s.tempDir)
		}
		return bddCtx, nil
	})

	sc.Step(`^the session has a system prompt and loaded skills$`, s.theSessionHasASystemPromptAndLoadedSkills)
	sc.Step(`^the session has an agent ID of "([^"]*)"$`, s.theSessionHasAnAgentIDOf)
	sc.Step(`^the session should contain a non-empty system prompt$`, s.theSessionShouldContainANonEmptySystemPrompt)
	sc.Step(`^the session should contain loaded skills$`, s.theSessionShouldContainLoadedSkills)
	sc.Step(`^the session should contain agent ID "([^"]*)"$`, s.theSessionShouldContainAgentID)
	sc.Step(`^an existing session file without enrichment fields$`, s.anExistingSessionFileWithoutEnrichmentFields)
	sc.Step(`^I load the legacy session$`, s.iLoadTheLegacySession)
	sc.Step(`^the session should load successfully$`, s.theSessionShouldLoadSuccessfully)
	sc.Step(`^the system prompt should be empty$`, s.theSystemPromptShouldBeEmpty)
	sc.Step(`^the loaded skills should be empty$`, s.theLoadedSkillsShouldBeEmpty)
}

// theSessionHasASystemPromptAndLoadedSkills sets enrichment metadata to be used when saving.
//
// Expected:
//   - s.steps is a valid pointer to the shared StepDefinitions.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Sets SystemPrompt and LoadedSkills on s.steps.currentMeta.
func (s *SessionEnrichmentSteps) theSessionHasASystemPromptAndLoadedSkills() error {
	s.steps.currentMeta.SystemPrompt = "You are a test assistant with enrichment."
	s.steps.currentMeta.LoadedSkills = []string{"pre-action", "memory-keeper"}
	return nil
}

// theSessionHasAnAgentIDOf sets the agent ID in the enrichment metadata.
//
// Expected:
//   - agentID is a non-empty string identifying the agent.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Sets AgentID on s.steps.currentMeta.
func (s *SessionEnrichmentSteps) theSessionHasAnAgentIDOf(agentID string) error {
	s.steps.currentMeta.AgentID = agentID
	return nil
}

// theSessionShouldContainANonEmptySystemPrompt verifies the saved session has a system prompt.
//
// Expected:
//   - A session has been saved and reloaded via the shared StepDefinitions.
//
// Returns:
//   - An error if the session is not found or the system prompt is empty.
//
// Side effects:
//   - None.
func (s *SessionEnrichmentSteps) theSessionShouldContainANonEmptySystemPrompt() error {
	info := s.findSavedSessionInfo()
	if info == nil {
		return fmt.Errorf("session %q not found in list", s.steps.savedSessionID)
	}
	if info.SystemPrompt == "" {
		return errors.New("expected non-empty system prompt, got empty string")
	}
	return nil
}

// theSessionShouldContainLoadedSkills verifies the saved session has loaded skills.
//
// Expected:
//   - A session has been saved and reloaded via the shared StepDefinitions.
//
// Returns:
//   - An error if the session is not found or loaded skills is empty.
//
// Side effects:
//   - None.
func (s *SessionEnrichmentSteps) theSessionShouldContainLoadedSkills() error {
	info := s.findSavedSessionInfo()
	if info == nil {
		return fmt.Errorf("session %q not found in list", s.steps.savedSessionID)
	}
	if len(info.LoadedSkills) == 0 {
		return errors.New("expected non-empty loaded skills, got empty slice")
	}
	return nil
}

// theSessionShouldContainAgentID verifies the saved session has the expected agent ID.
//
// Expected:
//   - expected is the anticipated agent identifier string.
//   - A session has been saved and reloaded via the shared StepDefinitions.
//
// Returns:
//   - An error if the session is not found or the agent ID does not match.
//
// Side effects:
//   - None.
func (s *SessionEnrichmentSteps) theSessionShouldContainAgentID(expected string) error {
	info := s.findSavedSessionInfo()
	if info == nil {
		return fmt.Errorf("session %q not found in list", s.steps.savedSessionID)
	}
	if info.AgentID != expected {
		return fmt.Errorf("expected agent ID %q, got %q", expected, info.AgentID)
	}
	return nil
}

// anExistingSessionFileWithoutEnrichmentFields creates a legacy session JSON file.
//
// Returns:
//   - An error if the temp directory or session file cannot be created.
//
// Side effects:
//   - Creates a temporary directory with a minimal JSON session file.
func (s *SessionEnrichmentSteps) anExistingSessionFileWithoutEnrichmentFields() error {
	tmpDir, err := os.MkdirTemp("", "flowstate-legacy-session-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	s.tempDir = tmpDir

	legacySession := map[string]interface{}{
		"session_id":      "legacy-session-001",
		"title":           "Legacy Session",
		"agent_id":        "",
		"embedding_model": "test-model",
		"last_active":     time.Now().Format(time.RFC3339),
		"messages": []map[string]interface{}{
			{"id": "msg-1", "role": "user", "content": "Hello from legacy"},
			{"id": "msg-2", "role": "assistant", "content": "Hi there!"},
		},
		"embeddings": []interface{}{},
	}

	data, err := json.MarshalIndent(legacySession, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling legacy session: %w", err)
	}

	sessionPath := filepath.Join(tmpDir, "legacy-session-001.json")
	if err := os.WriteFile(sessionPath, data, 0o600); err != nil {
		return fmt.Errorf("writing legacy session: %w", err)
	}

	s.legacySessionID = "legacy-session-001"

	sessionStore, err := ctxstore.NewFileSessionStore(tmpDir)
	if err != nil {
		return fmt.Errorf("creating session store: %w", err)
	}
	s.sessionStore = sessionStore

	return nil
}

// iLoadTheLegacySession attempts to load the legacy session file.
//
// Returns:
//   - nil (the load error is stored for later assertion).
//
// Side effects:
//   - Sets s.reloadedContextStore and s.loadError, and populates s.sessionInfo.
func (s *SessionEnrichmentSteps) iLoadTheLegacySession() error {
	store, err := s.sessionStore.Load(s.legacySessionID)
	s.reloadedContextStore = store
	s.loadError = err
	s.sessionInfo = s.sessionStore.List()
	return nil
}

// theSessionShouldLoadSuccessfully verifies the legacy session loaded without error.
//
// Returns:
//   - An error if loading failed.
//
// Side effects:
//   - None.
func (s *SessionEnrichmentSteps) theSessionShouldLoadSuccessfully() error {
	if s.loadError != nil {
		return fmt.Errorf("expected session to load successfully, got: %w", s.loadError)
	}
	if s.reloadedContextStore == nil {
		return errors.New("expected non-nil context store after loading")
	}
	return nil
}

// theSystemPromptShouldBeEmpty verifies the legacy session has an empty system prompt.
//
// Returns:
//   - An error if the system prompt is not empty.
//
// Side effects:
//   - None.
func (s *SessionEnrichmentSteps) theSystemPromptShouldBeEmpty() error {
	info := s.findSessionInfo(s.legacySessionID)
	if info == nil {
		return fmt.Errorf("session %q not found in list", s.legacySessionID)
	}
	if info.SystemPrompt != "" {
		return fmt.Errorf("expected empty system prompt, got %q", info.SystemPrompt)
	}
	return nil
}

// theLoadedSkillsShouldBeEmpty verifies the legacy session has no loaded skills.
//
// Returns:
//   - An error if loaded skills is not empty.
//
// Side effects:
//   - None.
func (s *SessionEnrichmentSteps) theLoadedSkillsShouldBeEmpty() error {
	info := s.findSessionInfo(s.legacySessionID)
	if info == nil {
		return fmt.Errorf("session %q not found in list", s.legacySessionID)
	}
	if len(info.LoadedSkills) != 0 {
		return fmt.Errorf("expected empty loaded skills, got %v", info.LoadedSkills)
	}
	return nil
}

// findSessionInfo locates a SessionInfo by ID within the cached session list.
//
// Expected:
//   - id is a non-empty session identifier.
//
// Returns:
//   - A pointer to the matching SessionInfo, or nil if not found.
//
// Side effects:
//   - None.
func (s *SessionEnrichmentSteps) findSessionInfo(id string) *ctxstore.SessionInfo {
	for i := range s.sessionInfo {
		if s.sessionInfo[i].ID == id {
			return &s.sessionInfo[i]
		}
	}
	return nil
}

// findSavedSessionInfo retrieves SessionInfo for the most recently saved session.
//
// Expected:
//   - s.steps.sessionStore is not nil and s.steps.savedSessionID is not empty.
//
// Returns:
//   - A pointer to the matching SessionInfo, or nil if not found.
//
// Side effects:
//   - None.
func (s *SessionEnrichmentSteps) findSavedSessionInfo() *ctxstore.SessionInfo {
	if s.steps.sessionStore == nil || s.steps.savedSessionID == "" {
		return nil
	}
	sessions := s.steps.sessionStore.List()
	for i := range sessions {
		if sessions[i].ID == s.steps.savedSessionID {
			return &sessions[i]
		}
	}
	return nil
}
