package support

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/config"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/discovery"
	"github.com/baphled/flowstate/internal/provider"
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

	ollamaProvider     *MockProvider
	providerName       string
	models             []Model
	chatRequest        *ChatRequest
	chatResponse       *ChatResponse
	streamChunks       []StreamChunk
	embeddings         []float64
	embeddingInputText string

	agentDiscovery    *discovery.AgentDiscovery
	suggestions       []discovery.AgentSuggestion
	contextStore      *ctxstore.FileContextStore
	tokenCounter      ctxstore.TokenCounter
	windowBuilder     *ctxstore.ContextWindowBuilder
	tokenBudget       int
	lastContextWindow []provider.Message
	inputBuffer       string
	isInsertMode      bool
	responseParts     []string
	currentAgent      string
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
	ctx.Step(`^I received a response$`, s.iReceivedAResponse)
	ctx.Step(`^I should see "([^"]*)" in the response$`, s.iShouldSeeInTheResponse)
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

	// Ollama provider steps
	ctx.Step(`^the Ollama provider is configured$`, s.theOllamaProviderIsConfigured)
	ctx.Step(`^I request the provider name$`, s.iRequestTheProviderName)
	ctx.Step(`^it should return "([^"]*)"$`, s.itShouldReturn)
	ctx.Step(`^Ollama has models available$`, s.ollamaHasModelsAvailable)
	ctx.Step(`^I request the list of models$`, s.iRequestTheListOfModels)
	ctx.Step(`^I should receive a list of models with context lengths$`, s.iShouldReceiveAListOfModelsWithContextLengths)
	ctx.Step(`^a valid chat request with messages$`, s.aValidChatRequestWithMessages)
	ctx.Step(`^I send the chat request to the Ollama provider$`, s.iSendTheChatRequestToTheOllamaProvider)
	ctx.Step(`^I should receive a chat response with a message$`, s.iShouldReceiveAChatResponseWithAMessage)
	ctx.Step(`^I stream the chat request to the Ollama provider$`, s.iStreamTheChatRequestToTheOllamaProvider)
	ctx.Step(`^I should receive stream chunks until done$`, s.iShouldReceiveStreamChunksUntilDone)
	ctx.Step(`^text to embed$`, s.textToEmbed)
	ctx.Step(`^I request embeddings from the Ollama provider$`, s.iRequestEmbeddingsFromTheOllamaProvider)
	ctx.Step(`^I should receive a vector of floats$`, s.iShouldReceiveAVectorOfFloats)
}

func (s *StepDefinitions) flowstateIsRunning() error {
	s.app = &TestApp{provider: NewMockProvider()}
	s.session = &TestSession{embeddings: make(map[string][]float64)}
	s.tokenCounter = ctxstore.NewApproximateCounter()
	s.windowBuilder = ctxstore.NewContextWindowBuilder(s.tokenCounter)
	return nil
}

func (s *StepDefinitions) ollamaIsAvailableWithModel(model string) error {
	s.app.provider = NewMockProvider()
	s.app.provider.name = "ollama"
	s.app.provider.models = []Model{
		{ID: model, Provider: "ollama", ContextLength: 4096},
	}
	s.app.provider.responses = []string{"Hello! I'm ready to help you."}
	return nil
}

func (s *StepDefinitions) iAmInInsertMode() error {
	s.isInsertMode = true
	s.inputBuffer = ""
	return nil
}

func (s *StepDefinitions) iType(text string) error {
	s.inputBuffer = text
	return nil
}

func (s *StepDefinitions) iPressEnter() error {
	if s.inputBuffer == "" {
		return nil
	}

	msg := Message{Role: "user", Content: s.inputBuffer}
	s.app.messages = append(s.app.messages, msg)

	ch, err := s.app.provider.Stream(s.ctx, ChatRequest{
		Model:    "mock",
		Messages: s.app.messages,
	})
	if err != nil {
		return err
	}

	s.responseParts = nil
	for chunk := range ch {
		s.responseParts = append(s.responseParts, chunk.Content)
	}

	var responseContent strings.Builder
	for _, part := range s.responseParts {
		responseContent.WriteString(part)
	}
	s.app.messages = append(s.app.messages, Message{Role: "assistant", Content: responseContent.String()})

	s.inputBuffer = ""
	return nil
}

func (s *StepDefinitions) iShouldSeeTokensAppearing() error {
	if len(s.responseParts) == 0 {
		return fmt.Errorf("expected streaming tokens, but received none")
	}
	return nil
}

func (s *StepDefinitions) iShouldSeeACompleteResponse() error {
	if len(s.app.messages) < 2 {
		return fmt.Errorf("expected at least user message and response, got %d messages", len(s.app.messages))
	}
	lastMsg := s.app.messages[len(s.app.messages)-1]
	if lastMsg.Role != "assistant" {
		return fmt.Errorf("expected assistant response, got %s", lastMsg.Role)
	}
	if lastMsg.Content == "" {
		return fmt.Errorf("expected non-empty response")
	}
	return nil
}

func (s *StepDefinitions) iHaveSentTheMessage(message string) error {
	s.inputBuffer = message
	return s.iPressEnter()
}

func (s *StepDefinitions) iReceivedAResponse() error {
	if len(s.app.messages) < 2 {
		return fmt.Errorf("expected at least a user message and response")
	}
	lastMsg := s.app.messages[len(s.app.messages)-1]
	if lastMsg.Role != "assistant" {
		return fmt.Errorf("expected assistant response, got %s", lastMsg.Role)
	}
	return nil
}

func (s *StepDefinitions) iShouldSeeInTheResponse(expected string) error {
	if len(s.app.messages) < 1 {
		return fmt.Errorf("no messages found")
	}

	for i := len(s.app.messages) - 1; i >= 0; i-- {
		if s.app.messages[i].Role == "assistant" {
			if strings.Contains(s.app.messages[i].Content, expected) {
				return nil
			}
			return fmt.Errorf("expected %q in response, got: %s", expected, s.app.messages[i].Content)
		}
	}
	return fmt.Errorf("no assistant response found")
}

func (s *StepDefinitions) theAgentIsAvailable(agentID string) error {
	manifests := []agent.AgentManifest{}
	if s.agentDiscovery != nil {
		return nil
	}

	switch agentID {
	case "coder":
		manifests = append(manifests, agent.AgentManifest{
			ID:   "coder",
			Name: "Coder Agent",
			Metadata: agent.Metadata{
				Role:      "Software development and coding tasks",
				Goal:      "Write clean, maintainable code",
				WhenToUse: "write code function parse JSON programming development",
			},
		})
	case "researcher":
		manifests = append(manifests, agent.AgentManifest{
			ID:   "researcher",
			Name: "Researcher Agent",
			Metadata: agent.Metadata{
				Role:      "Research and information gathering",
				Goal:      "Find and synthesise information",
				WhenToUse: "find information research investigate quantum computing learning",
			},
		})
	default:
		manifests = append(manifests, agent.AgentManifest{
			ID:   agentID,
			Name: agentID + " Agent",
			Metadata: agent.Metadata{
				Role:      "General purpose agent",
				Goal:      "Help with various tasks",
				WhenToUse: "general tasks",
			},
		})
	}

	s.agentDiscovery = discovery.NewAgentDiscovery(manifests)
	return nil
}

func (s *StepDefinitions) iAskForAgentSuggestionsFor(task string) error {
	if s.agentDiscovery == nil {
		s.agentDiscovery = discovery.NewAgentDiscovery([]agent.AgentManifest{})
	}
	s.suggestions = s.agentDiscovery.Suggest(task)
	return nil
}

func (s *StepDefinitions) iShouldReceiveAgentSuggestions() error {
	return nil
}

func (s *StepDefinitions) theSuggestionsShouldIncludeAnAgentWithConfidenceAbove(confidence float64) error {
	for _, suggestion := range s.suggestions {
		if suggestion.Confidence > confidence {
			return nil
		}
	}
	if len(s.suggestions) == 0 {
		return fmt.Errorf("no suggestions returned")
	}
	return fmt.Errorf("no agent found with confidence above %f, highest was %f", confidence, s.suggestions[0].Confidence)
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
	s.tokenBudget = limit
	s.tokenCounter = ctxstore.NewApproximateCounter()
	s.windowBuilder = ctxstore.NewContextWindowBuilder(s.tokenCounter)

	tempDir, err := os.MkdirTemp("", "context-store-*")
	if err != nil {
		return err
	}
	storePath := filepath.Join(tempDir, "context.json")

	store, err := ctxstore.NewFileContextStore(storePath, "nomic-embed-text")
	if err != nil {
		return err
	}
	s.contextStore = store
	return nil
}

func (s *StepDefinitions) iHaveExchangedMessages(count int) error {
	if s.contextStore == nil {
		return fmt.Errorf("context store not initialised, call 'a general agent with N token context limit' first")
	}
	for i := 0; i < count; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		content := fmt.Sprintf("Message %d content for testing context window", i+1)
		if i >= count-5 {
			content = fmt.Sprintf("This is a recent message %d about current topics", i+1)
		}
		s.contextStore.Append(provider.Message{
			Role:    role,
			Content: content,
		})
	}
	return nil
}

func (s *StepDefinitions) theNextMessageIsProcessed() error {
	if s.windowBuilder == nil || s.contextStore == nil {
		return fmt.Errorf("window builder or context store not initialised")
	}

	manifest := &agent.AgentManifest{
		ID:   "general",
		Name: "General Agent",
		Instructions: agent.Instructions{
			SystemPrompt: "You are a helpful assistant.",
		},
		ContextManagement: agent.DefaultContextManagement(),
	}

	result := s.windowBuilder.Build(manifest, s.contextStore, s.tokenBudget)
	s.lastContextWindow = result.Messages
	return nil
}

func (s *StepDefinitions) theContextWindowShouldUseLessThanTokens(limit int) error {
	if s.tokenCounter == nil {
		return fmt.Errorf("token counter not initialised")
	}

	totalTokens := 0
	for _, msg := range s.lastContextWindow {
		totalTokens += s.tokenCounter.Count(msg.Content)
	}

	if totalTokens >= limit {
		return fmt.Errorf("context window uses %d tokens, expected less than %d", totalTokens, limit)
	}
	return nil
}

func (s *StepDefinitions) iHaveAConversationAbout(topic string) error {
	if s.contextStore == nil {
		tempDir, err := os.MkdirTemp("", "context-store-*")
		if err != nil {
			return err
		}
		storePath := filepath.Join(tempDir, "context.json")

		store, err := ctxstore.NewFileContextStore(storePath, "nomic-embed-text")
		if err != nil {
			return err
		}
		s.contextStore = store
	}

	s.contextStore.Append(provider.Message{
		Role:    "user",
		Content: fmt.Sprintf("Let's talk about %s. I love making %s.", topic, topic),
	})
	s.contextStore.Append(provider.Message{
		Role:    "assistant",
		Content: fmt.Sprintf("Great! %s is a wonderful topic. Tell me more about your interest in %s.", topic, topic),
	})
	return nil
}

func (s *StepDefinitions) iLaterDiscussed(topic string) error {
	if s.contextStore == nil {
		return fmt.Errorf("context store not initialised")
	}

	s.contextStore.Append(provider.Message{
		Role:    "user",
		Content: fmt.Sprintf("Now let's discuss %s instead.", topic),
	})
	s.contextStore.Append(provider.Message{
		Role:    "assistant",
		Content: fmt.Sprintf("Sure, let's talk about %s.", topic),
	})
	return nil
}

func (s *StepDefinitions) iAskAbout(topic string) error {
	if s.contextStore == nil {
		return fmt.Errorf("context store not initialised")
	}
	if s.windowBuilder == nil {
		s.tokenCounter = ctxstore.NewApproximateCounter()
		s.windowBuilder = ctxstore.NewContextWindowBuilder(s.tokenCounter)
	}
	if s.tokenBudget == 0 {
		s.tokenBudget = 4096
	}

	manifest := &agent.AgentManifest{
		ID:   "general",
		Name: "General Agent",
		Instructions: agent.Instructions{
			SystemPrompt: "You are a helpful assistant.",
		},
		ContextManagement: agent.DefaultContextManagement(),
	}

	result := s.windowBuilder.Build(manifest, s.contextStore, s.tokenBudget)
	s.lastContextWindow = result.Messages
	return nil
}

func (s *StepDefinitions) theContextShouldIncludeMessagesAbout(topic string) error {
	for _, msg := range s.lastContextWindow {
		if strings.Contains(strings.ToLower(msg.Content), strings.ToLower(topic)) {
			return nil
		}
	}
	return fmt.Errorf("no messages about %q found in context window", topic)
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

// Ollama provider step implementations

func (s *StepDefinitions) theOllamaProviderIsConfigured() error {
	s.ollamaProvider = NewMockProvider()
	s.ollamaProvider.name = "ollama"
	s.ollamaProvider.models = []Model{
		{ID: "llama3.2", Provider: "ollama", ContextLength: 8192},
		{ID: "mistral", Provider: "ollama", ContextLength: 32768},
	}
	return nil
}

func (s *StepDefinitions) iRequestTheProviderName() error {
	if s.ollamaProvider == nil {
		return fmt.Errorf("ollama provider not configured")
	}
	s.providerName = s.ollamaProvider.Name()
	return nil
}

func (s *StepDefinitions) itShouldReturn(expected string) error {
	if s.providerName != expected {
		return fmt.Errorf("expected provider name %q, got %q", expected, s.providerName)
	}
	return nil
}

func (s *StepDefinitions) ollamaHasModelsAvailable() error {
	if s.ollamaProvider == nil {
		return fmt.Errorf("ollama provider not configured")
	}
	return nil
}

func (s *StepDefinitions) iRequestTheListOfModels() error {
	if s.ollamaProvider == nil {
		return fmt.Errorf("ollama provider not configured")
	}
	models, err := s.ollamaProvider.Models()
	if err != nil {
		return err
	}
	s.models = models
	return nil
}

func (s *StepDefinitions) iShouldReceiveAListOfModelsWithContextLengths() error {
	if len(s.models) == 0 {
		return fmt.Errorf("expected models, got none")
	}
	for _, m := range s.models {
		if m.ContextLength == 0 {
			return fmt.Errorf("expected context length for model %s", m.ID)
		}
	}
	return nil
}

func (s *StepDefinitions) aValidChatRequestWithMessages() error {
	s.chatRequest = &ChatRequest{
		Model: "llama3.2",
		Messages: []Message{
			{Role: "user", Content: "Hello, how are you?"},
		},
	}
	return nil
}

func (s *StepDefinitions) iSendTheChatRequestToTheOllamaProvider() error {
	if s.ollamaProvider == nil {
		return fmt.Errorf("ollama provider not configured")
	}
	if s.chatRequest == nil {
		return fmt.Errorf("no chat request provided")
	}
	resp, err := s.ollamaProvider.Chat(s.ctx, *s.chatRequest)
	if err != nil {
		return err
	}
	s.chatResponse = &resp
	return nil
}

func (s *StepDefinitions) iShouldReceiveAChatResponseWithAMessage() error {
	if s.chatResponse == nil {
		return fmt.Errorf("expected chat response, got nil")
	}
	if s.chatResponse.Message.Content == "" {
		return fmt.Errorf("expected message content, got empty")
	}
	return nil
}

func (s *StepDefinitions) iStreamTheChatRequestToTheOllamaProvider() error {
	if s.ollamaProvider == nil {
		return fmt.Errorf("ollama provider not configured")
	}
	if s.chatRequest == nil {
		return fmt.Errorf("no chat request provided")
	}
	ch, err := s.ollamaProvider.Stream(s.ctx, *s.chatRequest)
	if err != nil {
		return err
	}
	s.streamChunks = nil
	for chunk := range ch {
		s.streamChunks = append(s.streamChunks, chunk)
	}
	return nil
}

func (s *StepDefinitions) iShouldReceiveStreamChunksUntilDone() error {
	if len(s.streamChunks) == 0 {
		return fmt.Errorf("expected stream chunks, got none")
	}
	lastChunk := s.streamChunks[len(s.streamChunks)-1]
	if !lastChunk.Done {
		return fmt.Errorf("expected last chunk to be done")
	}
	return nil
}

func (s *StepDefinitions) textToEmbed() error {
	s.embeddingInputText = "This is sample text for embedding."
	return nil
}

func (s *StepDefinitions) iRequestEmbeddingsFromTheOllamaProvider() error {
	if s.ollamaProvider == nil {
		return fmt.Errorf("ollama provider not configured")
	}
	embeddings, err := s.ollamaProvider.Embed(s.ctx, EmbedRequest{
		Input: s.embeddingInputText,
		Model: "llama3.2",
	})
	if err != nil {
		return err
	}
	s.embeddings = embeddings
	return nil
}

func (s *StepDefinitions) iShouldReceiveAVectorOfFloats() error {
	if len(s.embeddings) == 0 {
		return fmt.Errorf("expected embeddings, got none")
	}
	return nil
}
