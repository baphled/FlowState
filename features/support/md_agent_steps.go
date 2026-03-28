package support

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
)

// RegisterMarkdownAgentSteps registers step definitions for markdown agent loading tests.
//
// Expected:
//   - ctx is a non-nil godog ScenarioContext for step registration.
//
// Side effects:
//   - Registers markdown-specific step patterns on the scenario context.
func (s *StepDefinitions) RegisterMarkdownAgentSteps(ctx *godog.ScenarioContext) {
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		s.lastError = nil
		return c, nil
	})

	ctx.Step(`^a markdown agent file "([^"]*)" with frontmatter containing id, name, and capabilities$`,
		s.aMarkdownAgentFileWithFrontmatterContainingIDNameAndCapabilities)
	ctx.Step(`^when the agent is loaded from the markdown file$`, s.whenTheAgentIsLoadedFromTheMarkdownFile)
	ctx.Step(`^the agent ID should be "([^"]*)"$`, s.theAgentIDShouldBe)
	ctx.Step(`^the agent name should match the frontmatter value$`, s.theAgentNameShouldMatchFrontmatterValue)
	ctx.Step(`^the agent capabilities should include the configured tools$`, s.theAgentCapabilitiesShouldIncludeTheConfiguredTools)

	ctx.Step(`^a markdown agent file with a body containing "([^"]*)"$`, s.aMarkdownAgentFileWithBodyContaining)

	ctx.Step(`^a markdown agent file with model_preferences for anthropic and ollama$`,
		s.aMarkdownAgentFileWithModelPreferencesForAnthropicAndOllama)
	ctx.Step(`^the model preferences should contain the anthropic provider$`,
		s.theModelPreferencesShouldContainTheAnthropicProvider)
	ctx.Step(`^the model preferences should contain the ollama provider$`,
		s.theModelPreferencesShouldContainTheOllamaProvider)

	ctx.Step(`^a markdown agent file "([^"]*)" without an id in frontmatter$`,
		s.aMarkdownAgentFileWithoutAnIDInFrontmatter)

	ctx.Step(`^a markdown agent file "([^"]*)" with id "([^"]*)" in frontmatter$`,
		s.aMarkdownAgentFileWithIDInFrontmatter)

	ctx.Step(`^a markdown agent file without context_management settings$`,
		s.aMarkdownAgentFileWithoutContextManagementSettings)
	ctx.Step(`^the context management should have default values$`,
		s.theContextManagementShouldHaveDefaultValues)
}

// aMarkdownAgentFileWithFrontmatterContainingIDNameAndCapabilities creates a markdown agent file with full frontmatter.
//
// Expected:
//   - filename is a valid markdown filename.
//
// Returns:
//   - nil on success, or an error if the file cannot be created.
//
// Side effects:
//   - Creates a temporary directory and markdown file with full frontmatter.
func (s *StepDefinitions) aMarkdownAgentFileWithFrontmatterContainingIDNameAndCapabilities(filename string) error {
	s.tempDir = filepath.Join(os.TempDir(), "flowstate-md-test")
	_ = os.RemoveAll(s.tempDir)
	if err := os.MkdirAll(s.tempDir, 0o750); err != nil {
		return err
	}

	content := `---
id: test-agent
name: Test Agent
schema_version: "1"
complexity: standard
capabilities:
  tools:
    - bash
    - read
  skills:
    - clean-code
  always_active_skills:
    - clean-code
metadata:
  role: Testing specialist
  goal: Verify agent functionality
  when_to_use: testing verification checks
---
You are a test specialist agent designed to verify functionality.
`

	s.markdownFilePath = filepath.Join(s.tempDir, filename)
	return os.WriteFile(s.markdownFilePath, []byte(content), 0o600)
}

// whenTheAgentIsLoadedFromTheMarkdownFile loads the markdown agent file.
//
// Expected:
//   - A markdown file path has been set.
//
// Returns:
//   - nil on success, or an error if loading fails.
//
// Side effects:
//   - Populates s.loadedManifest and s.lastError.
func (s *StepDefinitions) whenTheAgentIsLoadedFromTheMarkdownFile() error {
	if s.markdownFilePath == "" {
		return errors.New("no markdown file path set")
	}

	manifest, err := agent.LoadManifestMarkdown(s.markdownFilePath)
	if err != nil {
		s.lastError = err
		return err
	}

	s.loadedManifest = manifest
	return nil
}

// theAgentIDShouldBe verifies the agent ID matches the expected value.
//
// Expected:
//   - A manifest has been loaded.
//   - expectedID is the expected agent identifier.
//
// Returns:
//   - nil if ID matches, error otherwise.
//
// Side effects:
//   - None.
func (s *StepDefinitions) theAgentIDShouldBe(expectedID string) error {
	if s.loadedManifest == nil {
		return errors.New("no manifest loaded")
	}
	if s.loadedManifest.ID != expectedID {
		return fmt.Errorf("expected agent ID %q, got %q", expectedID, s.loadedManifest.ID)
	}
	return nil
}

// theAgentNameShouldMatchFrontmatterValue verifies the agent name matches frontmatter.
//
// Expected:
//   - A manifest has been loaded with a name set.
//
// Returns:
//   - nil if name matches, error otherwise.
//
// Side effects:
//   - None.
func (s *StepDefinitions) theAgentNameShouldMatchFrontmatterValue() error {
	if s.loadedManifest == nil {
		return errors.New("no manifest loaded")
	}
	if s.loadedManifest.Name == "" {
		return errors.New("agent name is empty")
	}
	if s.loadedManifest.Name != "Test Agent" {
		return fmt.Errorf("expected name %q, got %q", "Test Agent", s.loadedManifest.Name)
	}
	return nil
}

// theAgentCapabilitiesShouldIncludeTheConfiguredTools verifies capabilities are populated.
//
// Expected:
//   - A manifest has been loaded with capabilities configured.
//
// Returns:
//   - nil if tools are present, error otherwise.
//
// Side effects:
//   - None.
func (s *StepDefinitions) theAgentCapabilitiesShouldIncludeTheConfiguredTools() error {
	if s.loadedManifest == nil {
		return errors.New("no manifest loaded")
	}
	if len(s.loadedManifest.Capabilities.Tools) == 0 {
		return errors.New("expected tools in capabilities, got none")
	}
	hasBash := false
	for _, tool := range s.loadedManifest.Capabilities.Tools {
		if tool == "bash" {
			hasBash = true
			break
		}
	}
	if !hasBash {
		return fmt.Errorf("expected 'bash' tool in capabilities, got %v", s.loadedManifest.Capabilities.Tools)
	}
	return nil
}

// aMarkdownAgentFileWithBodyContaining creates a markdown file with body text.
//
// Expected:
//   - bodyText is the markdown body content.
//
// Returns:
//   - nil on success, or an error if the file cannot be created.
//
// Side effects:
//   - Creates a temporary directory and markdown file with the specified body.
func (s *StepDefinitions) aMarkdownAgentFileWithBodyContaining(bodyText string) error {
	s.tempDir = filepath.Join(os.TempDir(), "flowstate-md-body-test")
	_ = os.RemoveAll(s.tempDir)
	if err := os.MkdirAll(s.tempDir, 0o750); err != nil {
		return err
	}

	content := fmt.Sprintf(`---
id: body-test
name: Body Test Agent
schema_version: "1"
---
%s
`, bodyText)

	s.markdownFilePath = filepath.Join(s.tempDir, "body-test.md")
	return os.WriteFile(s.markdownFilePath, []byte(content), 0o600)
}

// aMarkdownAgentFileWithModelPreferencesForAnthropicAndOllama creates a markdown file with model preferences.
//
// Expected:
//   - No preconditions.
//
// Returns:
//   - nil on success, or an error if the file cannot be created.
//
// Side effects:
//   - Creates a temporary directory and markdown file with model preferences.
func (s *StepDefinitions) aMarkdownAgentFileWithModelPreferencesForAnthropicAndOllama() error {
	s.tempDir = filepath.Join(os.TempDir(), "flowstate-md-models-test")
	_ = os.RemoveAll(s.tempDir)
	if err := os.MkdirAll(s.tempDir, 0o750); err != nil {
		return err
	}

	content := `---
id: models-test
name: Models Test Agent
schema_version: "1"
model_preferences:
  anthropic:
    - provider: anthropic
      model: claude-opus-4-5
  ollama:
    - provider: ollama
      model: llama2
---
Agent with model preferences.
`

	s.markdownFilePath = filepath.Join(s.tempDir, "models-test.md")
	return os.WriteFile(s.markdownFilePath, []byte(content), 0o600)
}

// theModelPreferencesShouldContainTheAnthropicProvider verifies anthropic is in preferences.
//
// Expected:
//   - A manifest has been loaded with model preferences.
//
// Returns:
//   - nil if anthropic provider is present, error otherwise.
//
// Side effects:
//   - None.
func (s *StepDefinitions) theModelPreferencesShouldContainTheAnthropicProvider() error {
	if s.loadedManifest == nil {
		return errors.New("no manifest loaded")
	}
	if _, hasAnthropic := s.loadedManifest.ModelPreferences["anthropic"]; !hasAnthropic {
		return errors.New("expected anthropic provider in model preferences")
	}
	return nil
}

// theModelPreferencesShouldContainTheOllamaProvider verifies ollama is in preferences.
//
// Expected:
//   - A manifest has been loaded with model preferences.
//
// Returns:
//   - nil if ollama provider is present, error otherwise.
//
// Side effects:
//   - None.
func (s *StepDefinitions) theModelPreferencesShouldContainTheOllamaProvider() error {
	if s.loadedManifest == nil {
		return errors.New("no manifest loaded")
	}
	if _, hasOllama := s.loadedManifest.ModelPreferences["ollama"]; !hasOllama {
		return errors.New("expected ollama provider in model preferences")
	}
	return nil
}

// aMarkdownAgentFileWithoutAnIDInFrontmatter creates a markdown file without an ID field.
//
// Expected:
//   - filename is a valid markdown filename.
//
// Returns:
//   - nil on success, or an error if the file cannot be created.
//
// Side effects:
//   - Creates a temporary directory and markdown file.
func (s *StepDefinitions) aMarkdownAgentFileWithoutAnIDInFrontmatter(filename string) error {
	s.tempDir = filepath.Join(os.TempDir(), "flowstate-md-derived-test")
	_ = os.RemoveAll(s.tempDir)
	if err := os.MkdirAll(s.tempDir, 0o750); err != nil {
		return err
	}

	content := `---
name: Derived ID Agent
schema_version: "1"
---
Agent with ID derived from filename.
`

	s.markdownFilePath = filepath.Join(s.tempDir, filename)
	return os.WriteFile(s.markdownFilePath, []byte(content), 0o600)
}

// aMarkdownAgentFileWithIDInFrontmatter creates a markdown file with a specific ID in frontmatter.
//
// Expected:
//   - filename is a valid markdown filename.
//   - agentID is a non-empty agent identifier.
//
// Returns:
//   - nil on success, or an error if the file cannot be created.
//
// Side effects:
//   - Creates a temporary directory and markdown file with the specified ID.
func (s *StepDefinitions) aMarkdownAgentFileWithIDInFrontmatter(filename, agentID string) error {
	s.tempDir = filepath.Join(os.TempDir(), "flowstate-md-precedence-test")
	_ = os.RemoveAll(s.tempDir)
	if err := os.MkdirAll(s.tempDir, 0o750); err != nil {
		return err
	}

	content := fmt.Sprintf(`---
id: %s
name: Precedence Test Agent
schema_version: "1"
---
Agent with ID %s taking precedence over filename.
`, agentID, agentID)

	s.markdownFilePath = filepath.Join(s.tempDir, filename)
	return os.WriteFile(s.markdownFilePath, []byte(content), 0o600)
}

// aMarkdownAgentFileWithoutContextManagementSettings creates a markdown file without context management.
//
// Expected:
//   - No preconditions.
//
// Returns:
//   - nil on success, or an error if the file cannot be created.
//
// Side effects:
//   - Creates a temporary directory and markdown file without context_management.
func (s *StepDefinitions) aMarkdownAgentFileWithoutContextManagementSettings() error {
	s.tempDir = filepath.Join(os.TempDir(), "flowstate-md-defaults-test")
	_ = os.RemoveAll(s.tempDir)
	if err := os.MkdirAll(s.tempDir, 0o750); err != nil {
		return err
	}

	content := `---
id: defaults-test
name: Defaults Test Agent
schema_version: "1"
---
Agent without explicit context management settings.
`

	s.markdownFilePath = filepath.Join(s.tempDir, "defaults-test.md")
	return os.WriteFile(s.markdownFilePath, []byte(content), 0o600)
}

// theContextManagementShouldHaveDefaultValues verifies context management defaults are applied.
//
// Expected:
//   - A manifest has been loaded.
//
// Returns:
//   - nil if context management has default values, error otherwise.
//
// Side effects:
//   - None.
func (s *StepDefinitions) theContextManagementShouldHaveDefaultValues() error {
	if s.loadedManifest == nil {
		return errors.New("no manifest loaded")
	}

	cm := s.loadedManifest.ContextManagement
	if cm.MaxRecursionDepth != 2 {
		return fmt.Errorf("expected max recursion depth 2, got %d", cm.MaxRecursionDepth)
	}
	if cm.SummaryTier != "quick" {
		return fmt.Errorf("expected summary tier 'quick', got %q", cm.SummaryTier)
	}
	if cm.SlidingWindowSize != 10 {
		return fmt.Errorf("expected sliding window size 10, got %d", cm.SlidingWindowSize)
	}
	if cm.CompactionThreshold != 0.75 {
		return fmt.Errorf("expected compaction threshold 0.75, got %f", cm.CompactionThreshold)
	}
	if cm.EmbeddingModel != "nomic-embed-text" {
		return fmt.Errorf("expected embedding model 'nomic-embed-text', got %q", cm.EmbeddingModel)
	}

	return nil
}
