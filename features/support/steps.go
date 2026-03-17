package support

import (
	"context"

	"github.com/cucumber/godog"
)

type StepDefinitions struct {
	ctx     context.Context
	app     *TestApp
	session *TestSession
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
		s.app = &TestApp{provider: &MockProvider{}}
		s.session = &TestSession{embeddings: make(map[string][]float64)}
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
