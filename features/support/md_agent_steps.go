//go:build e2e

package support

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/app"
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

	ctx.Step(`^a markdown agent file "([^"]*)" without an id in frontmatter$`,
		s.aMarkdownAgentFileWithoutAnIDInFrontmatter)

	ctx.Step(`^a markdown agent file "([^"]*)" with id "([^"]*)" in frontmatter$`,
		s.aMarkdownAgentFileWithIDInFrontmatter)

	ctx.Step(`^a markdown agent file without context_management settings$`,
		s.aMarkdownAgentFileWithoutContextManagementSettings)
	ctx.Step(`^the context management should have default values$`,
		s.theContextManagementShouldHaveDefaultValues)

	ctx.Step(`^a markdown agent file with frontmatter but no body content$`,
		s.aMarkdownAgentFileWithFrontmatterButNoBodyContent)
	ctx.Step(`^the loaded manifest system prompt should be empty$`,
		s.theLoadedManifestSystemPromptShouldBeEmpty)

	ctx.Step(`^an agent directory contains both "([^"]*)" and "([^"]*)" with the same agent ID$`,
		s.anAgentDirectoryContainsBothMarkdownAndJSONWithSameID)
	ctx.Step(`^the registry should contain exactly one agent with ID "([^"]*)"$`,
		s.theRegistryShouldContainExactlyOneAgentWithID)
	ctx.Step(`^the agent should have been loaded from the markdown file$`,
		s.theAgentShouldHaveBeenLoadedFromTheMarkdownFile)
	ctx.Step(`^an embedded source containing markdown agent files$`,
		s.anEmbeddedSourceContainingMarkdownAgentFiles)
	ctx.Step(`^the agents directory is seeded$`, s.theAgentsDirectoryIsSeeded)
	ctx.Step(`^the destination should contain the markdown agent files$`,
		s.theDestinationShouldContainTheMarkdownAgentFiles)
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

// aMarkdownAgentFileWithFrontmatterButNoBodyContent creates a markdown file with frontmatter but empty body.
//
// Expected:
//   - No preconditions.
//
// Returns:
//   - nil on success, or an error if the file cannot be created.
//
// Side effects:
//   - Creates a temporary directory and markdown file with frontmatter only.
func (s *StepDefinitions) aMarkdownAgentFileWithFrontmatterButNoBodyContent() error {
	s.tempDir = filepath.Join(os.TempDir(), "flowstate-md-empty-body-test")
	_ = os.RemoveAll(s.tempDir)
	if err := os.MkdirAll(s.tempDir, 0o750); err != nil {
		return err
	}

	content := `---
id: empty-body-test
name: Empty Body Test Agent
schema_version: "1"
---
`

	s.markdownFilePath = filepath.Join(s.tempDir, "empty-body-test.md")
	return os.WriteFile(s.markdownFilePath, []byte(content), 0o600)
}

// theLoadedManifestSystemPromptShouldBeEmpty verifies that the loaded manifest's system prompt is empty.
//
// Expected:
//   - A manifest has been loaded.
//
// Returns:
//   - nil if system prompt is empty, error otherwise.
//
// Side effects:
//   - None.
func (s *StepDefinitions) theLoadedManifestSystemPromptShouldBeEmpty() error {
	if s.loadedManifest == nil {
		return errors.New("no manifest loaded")
	}
	if s.loadedManifest.Instructions.SystemPrompt != "" {
		return fmt.Errorf("expected empty system prompt, got %q", s.loadedManifest.Instructions.SystemPrompt)
	}
	return nil
}

// anAgentDirectoryContainsBothMarkdownAndJSONWithSameID creates a directory with both .md and .json manifests for the same agent.
//
// Expected:
//   - mdFilename is a markdown filename.
//   - jsonFilename is a JSON filename.
//
// Returns:
//   - nil on success, or an error if files cannot be created.
//
// Side effects:
//   - Creates a temporary directory with both manifest files.
func (s *StepDefinitions) anAgentDirectoryContainsBothMarkdownAndJSONWithSameID(mdFilename, jsonFilename string) error {
	s.tempDir = filepath.Join(os.TempDir(), "flowstate-precedence-test")
	_ = os.RemoveAll(s.tempDir)
	if err := os.MkdirAll(s.tempDir, 0o750); err != nil {
		return err
	}

	mdContent := `---
id: explorer
name: Explorer Agent
schema_version: "1"
---
Markdown version of the Explorer agent.
`

	jsonContent := `{
  "schema_version": "1",
  "id": "explorer",
  "name": "Explorer Agent",
  "instructions": {
    "system_prompt": "JSON version of the Explorer agent"
  }
}`

	if err := os.WriteFile(filepath.Join(s.tempDir, mdFilename), []byte(mdContent), 0o600); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(s.tempDir, jsonFilename), []byte(jsonContent), 0o600); err != nil {
		return err
	}

	s.agentRegistry = agent.NewRegistry()
	return nil
}

// theRegistryShouldContainExactlyOneAgentWithID verifies that exactly one agent with the given ID is in the registry.
//
// Expected:
//   - s.agentRegistry has been initialised.
//   - The registry has discovered agents.
//
// Returns:
//   - nil if exactly one agent with the ID exists, error otherwise.
//
// Side effects:
//   - None.
func (s *StepDefinitions) theRegistryShouldContainExactlyOneAgentWithID(id string) error {
	if err := s.agentRegistry.Discover(s.tempDir); err != nil {
		return fmt.Errorf("discovering agents: %w", err)
	}

	manifests := s.agentRegistry.List()
	count := 0
	for _, m := range manifests {
		if m.ID == id {
			count++
		}
	}

	if count != 1 {
		return fmt.Errorf("expected exactly 1 agent with ID %q, found %d", id, count)
	}

	return nil
}

// theAgentShouldHaveBeenLoadedFromTheMarkdownFile verifies that the agent was loaded from a markdown file.
//
// Expected:
//   - The registry has discovered agents.
//   - An explorer agent exists in the registry.
//
// Returns:
//   - nil if the agent's system prompt contains "Markdown version", error otherwise.
//
// Side effects:
//   - None.
func (s *StepDefinitions) theAgentShouldHaveBeenLoadedFromTheMarkdownFile() error {
	manifest, ok := s.agentRegistry.Get("explorer")
	if !ok {
		return errors.New("explorer agent not found in registry")
	}

	if !strings.Contains(manifest.Instructions.SystemPrompt, "Markdown version") {
		return fmt.Errorf("expected markdown version in system prompt, got: %q", manifest.Instructions.SystemPrompt)
	}

	return nil
}

// anEmbeddedSourceContainingMarkdownAgentFiles creates an in-memory filesystem with markdown agent files.
//
// Expected:
//   - No preconditions.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Creates a temporary directory with markdown agent files for seeding.
func (s *StepDefinitions) anEmbeddedSourceContainingMarkdownAgentFiles() error {
	s.agentsConfigDir = filepath.Join(os.TempDir(), "flowstate-seed-src")
	_ = os.RemoveAll(s.agentsConfigDir)

	agentsDir := filepath.Join(s.agentsConfigDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o750); err != nil {
		return err
	}

	mdContent := `---
id: test-agent
name: Test Agent
schema_version: "1"
---
Test agent for seeding.
`

	if err := os.WriteFile(filepath.Join(agentsDir, "test-agent.md"), []byte(mdContent), 0o600); err != nil {
		return err
	}

	return nil
}

// theAgentsDirectoryIsSeeded seeds the agents directory from the embedded source.
//
// Expected:
//   - An embedded source has been created.
//
// Returns:
//   - nil on success, or an error if seeding fails.
//
// Side effects:
//   - Creates the destination directory and copies markdown files.
func (s *StepDefinitions) theAgentsDirectoryIsSeeded() error {
	s.agentsWorkingDir = filepath.Join(os.TempDir(), "flowstate-seed-dest")
	_ = os.RemoveAll(s.agentsWorkingDir)

	srcFS := os.DirFS(s.agentsConfigDir)

	if err := app.SeedAgentsDir(srcFS, s.agentsWorkingDir); err != nil {
		return fmt.Errorf("seeding agents directory: %w", err)
	}

	return nil
}

// theDestinationShouldContainTheMarkdownAgentFiles verifies that markdown files were copied to the destination.
//
// Expected:
//   - Seeding has completed.
//
// Returns:
//   - nil if markdown files exist in the destination, error otherwise.
//
// Side effects:
//   - None.
func (s *StepDefinitions) theDestinationShouldContainTheMarkdownAgentFiles() error {
	mdFile := filepath.Join(s.agentsWorkingDir, "test-agent.md")
	if _, err := os.Stat(mdFile); err != nil {
		return fmt.Errorf("markdown file not found in destination: %w", err)
	}

	content, err := os.ReadFile(mdFile)
	if err != nil {
		return fmt.Errorf("reading markdown file: %w", err)
	}

	if !strings.Contains(string(content), "Test agent for seeding") {
		return errors.New("markdown file content mismatch")
	}

	return nil
}
