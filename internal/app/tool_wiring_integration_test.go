package app

import (
	"context"
	"testing"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// spyProvider captures the ChatRequest sent to the provider for assertion in tests.
type spyProvider struct {
	name            string
	capturedRequest *provider.ChatRequest
}

func (s *spyProvider) Name() string { return s.name }
func (s *spyProvider) Stream(_ context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	s.capturedRequest = &req
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}
func (s *spyProvider) Chat(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	s.capturedRequest = &req
	return provider.ChatResponse{}, nil
}
func (s *spyProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, errMockNotImplemented
}
func (s *spyProvider) Models() ([]provider.Model, error) { return nil, nil }

// TestToolWiringIntegration_DelegateToolAppearsInProviderRequest verifies that when
// the engine streams as a planner agent (can_delegate=true), the delegate tool
// appears in the ChatRequest tools list sent to the provider.
func TestToolWiringIntegration_DelegateToolAppearsInProviderRequest(t *testing.T) {
	executorManifest := agent.Manifest{
		ID:   "executor",
		Name: "Executor",
		Delegation: agent.Delegation{
			CanDelegate: false,
		},
	}

	plannerManifest := agent.Manifest{
		ID:   "planner",
		Name: "Planner",
		Delegation: agent.Delegation{
			CanDelegate: true,
		},
	}

	agentReg := agent.NewRegistry()
	agentReg.Register(&executorManifest)
	agentReg.Register(&plannerManifest)

	spy := &spyProvider{name: "spy"}
	providerReg := provider.NewRegistry()
	providerReg.Register(spy)

	application := &App{
		Registry:         agentReg,
		providerRegistry: providerReg,
	}

	var eng *engine.Engine
	var ensureToolsFn func(agent.Manifest)

	twc := &toolWiringCallbacks{
		hasTool: func(name string) bool {
			if eng == nil {
				return false
			}
			return eng.HasTool(name)
		},
		ensureTools: func(m agent.Manifest) {
			if ensureToolsFn != nil {
				ensureToolsFn(m)
			}
		},
		schemaRebuilder: func() []provider.Tool {
			if eng == nil {
				return nil
			}
			return eng.ToolSchemas()
		},
	}

	hookChain := buildHookChain(nil, func() agent.Manifest {
		if eng != nil {
			return eng.Manifest()
		}
		return executorManifest
	}, nil, nil, twc)

	eng = engine.New(engine.Config{
		Manifest:      executorManifest,
		AgentRegistry: agentReg,
		Registry:      providerReg,
		ChatProvider:  spy,
		HookChain:     hookChain,
	})

	application.wireDelegateToolIfEnabled(eng, executorManifest)

	ensureToolsFn = func(m agent.Manifest) {
		application.wireDelegateToolIfEnabled(eng, m)
	}

	_, err := eng.Stream(context.Background(), "planner", "hello")
	require.NoError(t, err)

	require.NotNil(t, spy.capturedRequest, "spy provider should have received a request")

	toolNames := make([]string, 0, len(spy.capturedRequest.Tools))
	for _, tool := range spy.capturedRequest.Tools {
		toolNames = append(toolNames, tool.Name)
	}
	assert.Contains(t, toolNames, "delegate",
		"provider request tools should contain 'delegate' when streaming as planner agent (can_delegate=true); got: %v", toolNames)
}

// TestToolWiringIntegration_ExecutorReceivesNoDelegateToolInRequest verifies that
// when streaming as executor (can_delegate=false), the delegate tool is NOT included
// in the ChatRequest tools list.
func TestToolWiringIntegration_ExecutorReceivesNoDelegateToolInRequest(t *testing.T) {
	executorManifest := agent.Manifest{
		ID:   "executor",
		Name: "Executor",
		Delegation: agent.Delegation{
			CanDelegate: false,
		},
	}

	agentReg := agent.NewRegistry()
	agentReg.Register(&executorManifest)

	spy := &spyProvider{name: "spy"}
	providerReg := provider.NewRegistry()
	providerReg.Register(spy)

	application := &App{
		Registry:         agentReg,
		providerRegistry: providerReg,
	}

	var eng *engine.Engine
	var ensureToolsFn func(agent.Manifest)

	twc := &toolWiringCallbacks{
		hasTool: func(name string) bool {
			if eng == nil {
				return false
			}
			return eng.HasTool(name)
		},
		ensureTools: func(m agent.Manifest) {
			if ensureToolsFn != nil {
				ensureToolsFn(m)
			}
		},
		schemaRebuilder: func() []provider.Tool {
			if eng == nil {
				return nil
			}
			return eng.ToolSchemas()
		},
	}

	hookChain := buildHookChain(nil, func() agent.Manifest {
		if eng != nil {
			return eng.Manifest()
		}
		return executorManifest
	}, nil, nil, twc)

	eng = engine.New(engine.Config{
		Manifest:      executorManifest,
		AgentRegistry: agentReg,
		Registry:      providerReg,
		ChatProvider:  spy,
		HookChain:     hookChain,
	})

	application.wireDelegateToolIfEnabled(eng, executorManifest)

	ensureToolsFn = func(m agent.Manifest) {
		application.wireDelegateToolIfEnabled(eng, m)
	}

	_, err := eng.Stream(context.Background(), "executor", "hello")
	require.NoError(t, err)

	require.NotNil(t, spy.capturedRequest, "spy provider should have received a request")

	toolNames := make([]string, 0, len(spy.capturedRequest.Tools))
	for _, tool := range spy.capturedRequest.Tools {
		toolNames = append(toolNames, tool.Name)
	}
	assert.NotContains(t, toolNames, "delegate",
		"executor agent (can_delegate=false) should not have delegate tool in request; got: %v", toolNames)
}
