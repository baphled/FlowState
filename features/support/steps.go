package support

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/config"
	"github.com/cucumber/godog"
)

type StepDefinitions struct {
	ctx           context.Context
	app           *TestApp
	session       *TestSession
	config        *config.AppConfig
	agentRegistry *agent.AgentRegistry
	tempDir       string
	configPath    string
}

type TestApp struct {
	provider *MockProvider
	agent    string
	messages []Message
}

type TestSession struct {
	id         string
	messages   []Message
	embeddings map[string][]float64
}

type Message struct {
	Role    string
	Content string
	ID      string
}

func (s *StepDefinitions) RegisterSteps(ctx *godog.ScenarioContext) {
	ctx.Before(func(ctx context.Context, sc *godog.Scenario) (context.Context, error) {
		s.ctx = ctx
		s.app = &TestApp{provider: NewMockProvider()}
		s.session = &TestSession{embeddings: make(map[string][]float64)}
		s.config = nil
		s.agentRegistry = nil
		s.tempDir = ""
		s.configPath = ""
		return ctx, nil
	})

	ctx.Step(`^FlowState is running$`, s.flowstateIsRunning)
	ctx.Step(`^Ollama is available with model "([^"]*)"$`, s.ollamaIsAvailableWithModel)
	ctx.Step(`^I am in insert mode$`, s.iAmInInsertMode)
	ctx.Step(`^I type "([^"]*)"$`, s.iType)
	ctx.Step(`^I press Enter$`, s.iPressEnter)
	ctx.Step(`^I should see tokens appearing$`, s.iShouldSeeTokensAppearing)
	ctx.Step(`^I should see a complete response$`, s.iShouldSeeACompleteResponse)
	ctx.Step(`^I have sent the message "([^"]*)"$`, s.iHaveSentTheMessage)
	ctx.Step(`^the agent "([^"]*)" is available$`, s.theAgentIsAvailable)
	ctx.Step(`^I ask for agent suggestions for "([^"]*)"$`, s.iAskForAgentSuggestionsFor)
	ctx.Step(`^I should receive agent suggestions$`, s.iShouldReceiveAgentSuggestions)
	ctx.Step(`^the suggestions should include an agent with confidence above (\d+\.?\d*)$`, s.theSuggestionsShouldIncludeAnAgentWithConfidenceAbove)
	ctx.Step(`^I am chatting with the "([^"]*)" agent$`, s.iAmChattingWithTheAgent)
	ctx.Step(`^I switch to the "([^"]*)" agent$`, s.iSwitchToTheAgent)
	ctx.Step(`^the active agent should be "([^"]*)"$`, s.theActiveAgentShouldBe)
	ctx.Step(`^the HTTP server is running on port (\d+)$`, s.theHTTPServerIsRunningOnPort)
	ctx.Step(`^I POST to "([^"]*)" with message "([^"]*)"$`, s.iPOSTToWithMessage)
	ctx.Step(`^I should receive an SSE stream$`, s.iShouldReceiveAnSSEStream)
	ctx.Step(`^the stream should contain chunks with content$`, s.theStreamShouldContainChunksWithContent)
	ctx.Step(`^a general agent with (\d+) token context limit$`, s.aGeneralAgentWithTokenContextLimit)
	ctx.Step(`^I have exchanged (\d+) messages$`, s.iHaveExchangedMessages)
	ctx.Step(`^the next message is processed$`, s.theNextMessageIsProcessed)
	ctx.Step(`^the context window should use less than (\d+) tokens$`, s.theContextWindowShouldUseLessThanTokens)
	ctx.Step(`^I have a conversation about "([^"]*)"$`, s.iHaveAConversationAbout)
	ctx.Step(`^I later discussed "([^"]*)"$`, s.iLaterDiscussed)
	ctx.Step(`^I ask about "([^"]*)"$`, s.iAskAbout)
	ctx.Step(`^the context should include messages about "([^"]*)"$`, s.theContextShouldIncludeMessagesAbout)
	ctx.Step(`^I have an active session with messages$`, s.iHaveAnActiveSessionWithMessages)
	ctx.Step(`^I save the session$`, s.iSaveTheSession)
	ctx.Step(`^I reload the session$`, s.iReloadTheSession)
	ctx.Step(`^all messages should be restored$`, s.allMessagesShouldBeRestored)
	ctx.Step(`^embedding vectors should be preserved$`, s.embeddingVectorsShouldBePreserved)

	// Config steps
	ctx.Step(`^no FlowState configuration file exists$`, s.noFlowStateConfigurationFileExists)
	ctx.Step(`^FlowState loads its configuration$`, s.flowstateLoadsItsConfiguration)
	ctx.Step(`^the default configuration should be used$`, s.theDefaultConfigurationShouldBeUsed)
	ctx.Step(`^a FlowState configuration file exists at "([^"]*)"$`, s.aFlowStateConfigurationFileExistsAt)
	ctx.Step(`^FlowState loads configuration from that file path$`, s.flowstateLoadsConfigurationFromThatFilePath)
	ctx.Step(`^the configuration from that file should be used$`, s.theConfigurationFromThatFileShouldBeUsed)
	ctx.Step(`^FlowState has loaded its configuration$`, s.flowstateHasLoadedItsConfiguration)
	ctx.Step(`^the configuration should include provider settings$`, s.theConfigurationShouldIncludeProviderSettings)
	ctx.Step(`^the configuration should include an agent directory$`, s.theConfigurationShouldIncludeAnAgentDirectory)
	ctx.Step(`^the configuration should include a skill directory$`, s.theConfigurationShouldIncludeASkillDirectory)
	ctx.Step(`^the configuration should include a data directory$`, s.theConfigurationShouldIncludeADataDirectory)
	ctx.Step(`^the configuration should include a log level$`, s.theConfigurationShouldIncludeALogLevel)

	// Agent registry steps
	ctx.Step(`^an agent directory contains valid JSON and Markdown agent manifests$`, s.anAgentDirectoryContainsValidJSONAndMarkdownAgentManifests)
	ctx.Step(`^the agent registry discovers agents from that directory$`, s.theAgentRegistryDiscoversAgentsFromThatDirectory)
	ctx.Step(`^the registry should include agents from both manifest formats$`, s.theRegistryShouldIncludeAgentsFromBothManifestFormats)
	ctx.Step(`^an empty agent directory$`, s.anEmptyAgentDirectory)
	ctx.Step(`^the registry should be empty$`, s.theRegistryShouldBeEmpty)
	ctx.Step(`^an agent directory contains valid and invalid agent manifests$`, s.anAgentDirectoryContainsValidAndInvalidAgentManifests)
	ctx.Step(`^the valid agents should be available in the registry$`, s.theValidAgentsShouldBeAvailableInTheRegistry)
	ctx.Step(`^the invalid agent manifests should be skipped$`, s.theInvalidAgentManifestsShouldBeSkipped)
}

func (s *StepDefinitions) flowstateIsRunning() error {
	return godog.ErrPending
}

func (s *StepDefinitions) ollamaIsAvailableWithModel(model string) error {
	return godog.ErrPending
}

func (s *StepDefinitions) iAmInInsertMode() error {
	return godog.ErrPending
}

func (s *StepDefinitions) iType(text string) error {
	return godog.ErrPending
}

func (s *StepDefinitions) iPressEnter() error {
	return godog.ErrPending
}

func (s *StepDefinitions) iShouldSeeTokensAppearing() error {
	return godog.ErrPending
}

func (s *StepDefinitions) iShouldSeeACompleteResponse() error {
	return godog.ErrPending
}

func (s *StepDefinitions) iHaveSentTheMessage(message string) error {
	return godog.ErrPending
}

func (s *StepDefinitions) theAgentIsAvailable(agentID string) error {
	return godog.ErrPending
}

func (s *StepDefinitions) iAskForAgentSuggestionsFor(task string) error {
	return godog.ErrPending
}

func (s *StepDefinitions) iShouldReceiveAgentSuggestions() error {
	return godog.ErrPending
}

func (s *StepDefinitions) theSuggestionsShouldIncludeAnAgentWithConfidenceAbove(confidence float64) error {
	return godog.ErrPending
}

func (s *StepDefinitions) iAmChattingWithTheAgent(agentID string) error {
	return godog.ErrPending
}

func (s *StepDefinitions) iSwitchToTheAgent(agentID string) error {
	return godog.ErrPending
}

func (s *StepDefinitions) theActiveAgentShouldBe(agentID string) error {
	return godog.ErrPending
}

func (s *StepDefinitions) theHTTPServerIsRunningOnPort(port int) error {
	return godog.ErrPending
}

func (s *StepDefinitions) iPOSTToWithMessage(endpoint, message string) error {
	return godog.ErrPending
}

func (s *StepDefinitions) iShouldReceiveAnSSEStream() error {
	return godog.ErrPending
}

func (s *StepDefinitions) theStreamShouldContainChunksWithContent() error {
	return godog.ErrPending
}

func (s *StepDefinitions) aGeneralAgentWithTokenContextLimit(limit int) error {
	return godog.ErrPending
}

func (s *StepDefinitions) iHaveExchangedMessages(count int) error {
	return godog.ErrPending
}

func (s *StepDefinitions) theNextMessageIsProcessed() error {
	return godog.ErrPending
}

func (s *StepDefinitions) theContextWindowShouldUseLessThanTokens(limit int) error {
	return godog.ErrPending
}

func (s *StepDefinitions) iHaveAConversationAbout(topic string) error {
	return godog.ErrPending
}

func (s *StepDefinitions) iLaterDiscussed(topic string) error {
	return godog.ErrPending
}

func (s *StepDefinitions) iAskAbout(topic string) error {
	return godog.ErrPending
}

func (s *StepDefinitions) theContextShouldIncludeMessagesAbout(topic string) error {
	return godog.ErrPending
}

func (s *StepDefinitions) iHaveAnActiveSessionWithMessages() error {
	return godog.ErrPending
}

func (s *StepDefinitions) iSaveTheSession() error {
	return godog.ErrPending
}

func (s *StepDefinitions) iReloadTheSession() error {
	return godog.ErrPending
}

func (s *StepDefinitions) allMessagesShouldBeRestored() error {
	return godog.ErrPending
}

func (s *StepDefinitions) embeddingVectorsShouldBePreserved() error {
	return godog.ErrPending
}

// Config step implementations

func (s *StepDefinitions) noFlowStateConfigurationFileExists() error {
	s.tempDir = filepath.Join(os.TempDir(), "flowstate-test-config")
	_ = os.RemoveAll(s.tempDir)
	return nil
}

func (s *StepDefinitions) flowstateLoadsItsConfiguration() error {
	nonExistentPath := filepath.Join(s.tempDir, "config.yaml")
	cfg, err := config.LoadConfigFromPath(nonExistentPath)
	if err != nil {
		return err
	}
	s.config = cfg
	return nil
}

func (s *StepDefinitions) theDefaultConfigurationShouldBeUsed() error {
	if s.config == nil {
		return fmt.Errorf("expected configuration to be loaded")
	}

	defaults := config.DefaultConfig()
	if s.config.Providers.Default != defaults.Providers.Default {
		return fmt.Errorf("expected default provider %q, got %q", defaults.Providers.Default, s.config.Providers.Default)
	}
	if s.config.AgentDir != defaults.AgentDir {
		return fmt.Errorf("expected agent dir %q, got %q", defaults.AgentDir, s.config.AgentDir)
	}
	if s.config.SkillDir != defaults.SkillDir {
		return fmt.Errorf("expected skill dir %q, got %q", defaults.SkillDir, s.config.SkillDir)
	}
	if s.config.DataDir != defaults.DataDir {
		return fmt.Errorf("expected data dir %q, got %q", defaults.DataDir, s.config.DataDir)
	}
	if s.config.LogLevel != defaults.LogLevel {
		return fmt.Errorf("expected log level %q, got %q", defaults.LogLevel, s.config.LogLevel)
	}

	return nil
}

func (s *StepDefinitions) aFlowStateConfigurationFileExistsAt(path string) error {
	s.tempDir = filepath.Dir(path)
	s.configPath = path
	if err := os.RemoveAll(s.tempDir); err != nil {
		return fmt.Errorf("removing config directory %q: %w", s.tempDir, err)
	}
	if err := os.MkdirAll(s.tempDir, 0o750); err != nil {
		return fmt.Errorf("creating config directory %q: %w", s.tempDir, err)
	}
	content := []byte(`providers:
  default: openai
log_level: debug
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return fmt.Errorf("writing config file %q: %w", path, err)
	}
	return nil
}

func (s *StepDefinitions) flowstateLoadsConfigurationFromThatFilePath() error {
	if s.configPath == "" {
		return fmt.Errorf("expected config path to be set")
	}

	cfg, err := config.LoadConfigFromPath(s.configPath)
	if err != nil {
		return err
	}
	s.config = cfg
	return nil
}

func (s *StepDefinitions) theConfigurationFromThatFileShouldBeUsed() error {
	if s.config == nil {
		return fmt.Errorf("expected configuration to be loaded")
	}
	if s.config.Providers.Default != "openai" {
		return fmt.Errorf("expected provider default \"openai\", got %q", s.config.Providers.Default)
	}
	if s.config.LogLevel != "debug" {
		return fmt.Errorf("expected log level \"debug\", got %q", s.config.LogLevel)
	}
	return nil
}

func (s *StepDefinitions) flowstateHasLoadedItsConfiguration() error {
	s.config = config.DefaultConfig()
	return nil
}

func (s *StepDefinitions) theConfigurationShouldIncludeProviderSettings() error {
	if s.config == nil {
		return fmt.Errorf("expected configuration to be loaded")
	}
	if s.config.Providers.Default == "" {
		return fmt.Errorf("expected default provider to be set")
	}
	if s.config.Providers.Ollama.Model == "" {
		return fmt.Errorf("expected ollama provider settings to be present")
	}
	return nil
}

func (s *StepDefinitions) theConfigurationShouldIncludeAnAgentDirectory() error {
	if s.config == nil {
		return fmt.Errorf("expected configuration to be loaded")
	}
	if s.config.AgentDir == "" {
		return fmt.Errorf("expected agent directory to be set")
	}
	return nil
}

func (s *StepDefinitions) theConfigurationShouldIncludeASkillDirectory() error {
	if s.config == nil {
		return fmt.Errorf("expected configuration to be loaded")
	}
	if s.config.SkillDir == "" {
		return fmt.Errorf("expected skill directory to be set")
	}
	return nil
}

func (s *StepDefinitions) theConfigurationShouldIncludeADataDirectory() error {
	if s.config == nil {
		return fmt.Errorf("expected configuration to be loaded")
	}
	if s.config.DataDir == "" {
		return fmt.Errorf("expected data directory to be set")
	}
	return nil
}

func (s *StepDefinitions) theConfigurationShouldIncludeALogLevel() error {
	if s.config == nil {
		return fmt.Errorf("expected configuration to be loaded")
	}
	if s.config.LogLevel == "" {
		return fmt.Errorf("expected log level to be set")
	}
	return nil
}

// Agent registry step implementations

func (s *StepDefinitions) anAgentDirectoryContainsValidJSONAndMarkdownAgentManifests() error {
	s.tempDir = filepath.Join(os.TempDir(), "flowstate-test-agents")
	_ = os.RemoveAll(s.tempDir)
	if err := os.MkdirAll(s.tempDir, 0o750); err != nil {
		return err
	}

	jsonContent := `{"id": "json-agent", "name": "JSON Agent"}`
	if err := os.WriteFile(filepath.Join(s.tempDir, "json-agent.json"), []byte(jsonContent), 0o600); err != nil {
		return err
	}

	mdContent := `---
description: Markdown Agent
mode: subagent
---
# Markdown Agent
`
	return os.WriteFile(filepath.Join(s.tempDir, "md-agent.md"), []byte(mdContent), 0o600)
}

func (s *StepDefinitions) theAgentRegistryDiscoversAgentsFromThatDirectory() error {
	s.agentRegistry = agent.NewAgentRegistry()
	return s.agentRegistry.Discover(s.tempDir)
}

func (s *StepDefinitions) theRegistryShouldIncludeAgentsFromBothManifestFormats() error {
	if s.agentRegistry == nil {
		return fmt.Errorf("expected agent registry to be created")
	}
	if len(s.agentRegistry.List()) < 2 {
		return fmt.Errorf("expected at least 2 agents, got %d", len(s.agentRegistry.List()))
	}
	_, hasJSON := s.agentRegistry.Get("json-agent")
	_, hasMD := s.agentRegistry.Get("md-agent")
	if !hasJSON || !hasMD {
		return fmt.Errorf("expected both json-agent and md-agent to be discovered")
	}
	return nil
}

func (s *StepDefinitions) anEmptyAgentDirectory() error {
	s.tempDir = filepath.Join(os.TempDir(), "flowstate-test-empty")
	_ = os.RemoveAll(s.tempDir)
	return os.MkdirAll(s.tempDir, 0o750)
}

func (s *StepDefinitions) theRegistryShouldBeEmpty() error {
	if s.agentRegistry == nil {
		return fmt.Errorf("expected agent registry to be created")
	}
	if len(s.agentRegistry.List()) != 0 {
		return fmt.Errorf("expected empty registry, got %d agents", len(s.agentRegistry.List()))
	}
	return nil
}

func (s *StepDefinitions) anAgentDirectoryContainsValidAndInvalidAgentManifests() error {
	s.tempDir = filepath.Join(os.TempDir(), "flowstate-test-mixed")
	_ = os.RemoveAll(s.tempDir)
	if err := os.MkdirAll(s.tempDir, 0o750); err != nil {
		return err
	}

	validContent := `{"id": "valid-agent", "name": "Valid Agent"}`
	if err := os.WriteFile(filepath.Join(s.tempDir, "valid.json"), []byte(validContent), 0o600); err != nil {
		return err
	}

	invalidContent := `{not valid json`
	return os.WriteFile(filepath.Join(s.tempDir, "invalid.json"), []byte(invalidContent), 0o600)
}

func (s *StepDefinitions) theValidAgentsShouldBeAvailableInTheRegistry() error {
	if s.agentRegistry == nil {
		return fmt.Errorf("expected agent registry to be created")
	}
	_, hasValid := s.agentRegistry.Get("valid-agent")
	if !hasValid {
		return fmt.Errorf("expected valid-agent to be discovered")
	}
	return nil
}

func (s *StepDefinitions) theInvalidAgentManifestsShouldBeSkipped() error {
	if s.agentRegistry == nil {
		return fmt.Errorf("expected agent registry to be created")
	}
	if len(s.agentRegistry.List()) != 1 {
		return fmt.Errorf("expected only valid manifests to remain, got %d agents", len(s.agentRegistry.List()))
	}
	return nil
}
