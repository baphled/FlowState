Feature: Markdown Agent Definitions
  As a FlowState user
  I want to define agents as single markdown files with YAML frontmatter
  So that I can create and customise agents without editing JSON

  @smoke
  Scenario: Load agent from markdown with full frontmatter
    Given a markdown agent file "test-agent.md" with frontmatter containing id, name, and capabilities
    When the agent is loaded from the markdown file
    Then the agent ID should be "test-agent"
    And the agent name should match the frontmatter value
    And the agent capabilities should include the configured tools

  Scenario: Markdown body becomes system prompt
    Given a markdown agent file with a body containing "You are a test specialist"
    When the agent is loaded from the markdown file
    Then the system prompt should contain "You are a test specialist"

  Scenario: Model preferences parsed from YAML map
    Given a markdown agent file with model_preferences for anthropic and ollama
    When the agent is loaded from the markdown file
    Then the model preferences should contain the anthropic provider
    And the model preferences should contain the ollama provider

  Scenario: Agent ID derived from filename when not in frontmatter
    Given a markdown agent file "derived-id.md" without an id in frontmatter
    When the agent is loaded from the markdown file
    Then the agent ID should be "derived-id"

  Scenario: Frontmatter ID takes precedence over filename
    Given a markdown agent file "wrong-name.md" with id "correct-id" in frontmatter
    When the agent is loaded from the markdown file
    Then the agent ID should be "correct-id"

  Scenario: Default context management applied to markdown agents
    Given a markdown agent file without context_management settings
    When the agent is loaded from the markdown file
    Then the context management should have default values
