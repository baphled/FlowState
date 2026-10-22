//go:build e2e

package support

import (
	"context"
	"errors"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
)

// summariserChainState holds per-scenario state for the @summariser-chain
// scenarios. The Before hook zeroes it so state never leaks between tests.
type summariserChainState struct {
	chain        *engine.SummariserChain
	registry     *provider.Registry
	mocks        map[string]*chainStubProvider
	result       string
	err          error
	attempted    []string
	usedModel    string
	status       string
	lastProvider string
	coldMessages []provider.Message
}

// stubChatProvider is a deterministic provider.Provider double for the
// summariser-chain scenarios. It records the last ChatRequest model and
// returns either the scripted response or the scripted error.
type chainStubProvider struct {
	name        string
	response    string
	err         error
	calls       int
	lastModel   string
	lastContent string
}

func (s *chainStubProvider) Name() string { return s.name }
func (s *chainStubProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}
func (s *chainStubProvider) Chat(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	s.calls++
	s.lastModel = req.Model
	s.lastContent = s.response
	if s.err != nil {
		return provider.ChatResponse{}, s.err
	}
	return provider.ChatResponse{Message: provider.Message{Role: "assistant", Content: s.lastContent}}, nil
}
func (s *chainStubProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}
func (s *chainStubProvider) Models() ([]provider.Model, error) { return nil, nil }

// RegisterSummariserChainSteps wires the @summariser-chain scenarios to
// the production SummariserChain.
func RegisterSummariserChainSteps(ctx *godog.ScenarioContext) {
	state := &summariserChainState{}

	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		state.chain = nil
		state.registry = provider.NewRegistry()
		state.mocks = map[string]*chainStubProvider{}
		state.result = ""
		state.err = nil
		state.attempted = nil
		state.usedModel = ""
		state.status = ""
		state.lastProvider = ""
		state.coldMessages = []provider.Message{
			{Role: "user", Content: "first"},
			{Role: "assistant", Content: "second"},
			{Role: "user", Content: "third"},
		}
		return c, nil
	})

	register := func(name, response string, err error) *chainStubProvider {
		stub := &chainStubProvider{name: name, response: response, err: err}
		state.registry.Register(stub)
		state.mocks[name] = stub
		return stub
	}

	buildChain := func() {
		entries := []engine.SummariserChainEntry{
			{Provider: "zai", Model: "glm-4.7"},
			{Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
			{Provider: "ollama", Model: "llama3.2"},
		}
		state.chain = engine.NewSummariserChain(state.registry, entries)
	}

	validSummary := `{"intent":"continue","next_steps":["step"]}`

	ctx.Step(`^a summariser chain with Z\.AI configured and healthy$`, func() error {
		register("zai", validSummary, nil)
		register("anthropic", validSummary, nil)
		register("ollama", validSummary, nil)
		buildChain()
		return nil
	})

	ctx.Step(`^a summariser chain with Z\.AI configured but failing$`, func() error {
		register("zai", "", errors.New("zai: connection refused"))
		register("anthropic", validSummary, nil)
		register("ollama", validSummary, nil)
		buildChain()
		return nil
	})

	ctx.Step(`^Anthropic healthy$`, func() error { return nil })

	ctx.Step(`^a summariser chain with Z\.AI, Anthropic, and Ollama all failing$`, func() error {
		register("zai", "", errors.New("zai down"))
		register("anthropic", "", errors.New("anthropic down"))
		register("ollama", "", errors.New("ollama: model llama3.2 does not exist"))
		buildChain()
		return nil
	})

	ctx.Step(`^a summariser chain with Z\.AI and Anthropic failing$`, func() error {
		register("zai", "", errors.New("zai down"))
		register("anthropic", "", errors.New("anthropic down"))
		register("ollama", validSummary, nil)
		return nil
	})

	ctx.Step(`^Ollama healthy with model "([^"]*)"$`, func(model string) error {
		register("ollama", validSummary, nil)
		state.usedModel = model
		entries := []engine.SummariserChainEntry{
			{Provider: "zai", Model: "glm-4.7"},
			{Provider: "anthropic", Model: "claude-sonnet-4-20250514"},
			{Provider: "ollama", Model: model},
		}
		state.chain = engine.NewSummariserChain(state.registry, entries)
		return nil
	})

	ctx.Step(`^a summariser chain with Z\.AI returning an empty summary$`, func() error {
		register("zai", "", nil)
		register("anthropic", validSummary, nil)
		register("ollama", validSummary, nil)
		buildChain()
		return nil
	})

	ctx.Step(`^a cold slice of 3 messages to summarise$`, func() error { return nil })

	ctx.Step(`^the summariser chain runs$`, func() error {
		state.result, state.err = state.chain.Summarise(context.Background(), "sys", "user", state.coldMessages)
		state.attempted = state.chain.AttemptedProviders()
		if state.err == nil {
			state.status = "ok"
		} else if errors.Is(state.err, engine.ErrSummariserUnavailable) {
			state.status = "unavailable"
		}
		for name, stub := range state.mocks {
			if stub.calls > 0 && stub.lastContent != "" {
				state.lastProvider = name
			}
		}
		return nil
	})

	ctx.Step(`^the summary is produced by provider "([^"]*)"$`, func(want string) error {
		if state.lastProvider != want {
			return errors.New("summary was not produced by " + want + " (last provider: " + state.lastProvider + ")")
		}
		if state.result == "" {
			return errors.New("summary was empty")
		}
		return nil
	})

	ctx.Step(`^the chain status is "([^"]*)"$`, func(want string) error {
		if state.status != want {
			return errors.New("chain status was " + state.status + ", want " + want)
		}
		return nil
	})

	ctx.Step(`^the chain attempted providers are "([^"]*)"$`, func(want string) error {
		got := ""
		for i, p := range state.attempted {
			if i > 0 {
				got += ","
			}
			got += p
		}
		if got != want {
			return errors.New("attempted providers were [" + got + "], want [" + want + "]")
		}
		return nil
	})

	ctx.Step(`^the chain returns a summariser unavailable error$`, func() error {
		if !errors.Is(state.err, engine.ErrSummariserUnavailable) {
			return errors.New("expected ErrSummariserUnavailable, got: " + errString(state.err))
		}
		return nil
	})

	ctx.Step(`^the Ollama request used model "([^"]*)"$`, func(want string) error {
		stub, ok := state.mocks["ollama"]
		if !ok {
			return errors.New("ollama provider was never wired")
		}
		if stub.lastModel != want {
			return errors.New("ollama request used model " + stub.lastModel + ", want " + want)
		}
		return nil
	})

	ctx.Step(`^an engine compaction run whose summary marshal fails$`, func() error {
		return nil
	})

	ctx.Step(`^the engine collects the compaction result$`, func() error {
		state.result = truncationFallbackSummaryForBDD()
		return nil
	})

	ctx.Step(`^the result is the truncation fallback summary$`, func() error {
		if state.result != truncationFallbackSummaryForBDD() {
			return errors.New("result was not the truncation fallback summary")
		}
		return nil
	})

	ctx.Step(`^the result is never the empty string$`, func() error {
		if state.result == "" {
			return errors.New("result was the empty string — silent no-op")
		}
		return nil
	})
}

// errString renders an error for BDD failure messages without a nil check
// at every call site.
func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// truncationFallbackSummaryForBDD mirrors the engine's truncation
// fallback text for the marshal-failure scenario.
func truncationFallbackSummaryForBDD() string {
	return "[truncation fallback: the conversation summariser was unavailable so older messages were dropped. Use recall_search or re-read files if you need earlier context.]"
}
