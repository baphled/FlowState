//go:build e2e

package support

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
)

var errFailoverMockNotImplemented = errors.New("failover mock: method not implemented")

// FailoverMockStreamProvider is a test double that implements the provider
// streaming interface for failover BDD scenarios. It returns a
// pre-configured error from Stream and "not implemented" from all other
// methods, mirroring the mockStreamProvider used in the package-level
// integration tests.
type FailoverMockStreamProvider struct {
	name     string
	streamFn func(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error)
}

// Name returns the provider name.
func (m *FailoverMockStreamProvider) Name() string { return m.name }

// Stream delegates to the configured streamFn.
func (m *FailoverMockStreamProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return m.streamFn(ctx, req)
}

// Chat is not implemented in the failover mock.
func (m *FailoverMockStreamProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, errFailoverMockNotImplemented
}

// Embed is not implemented in the failover mock.
func (m *FailoverMockStreamProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, errFailoverMockNotImplemented
}

// Models is not implemented in the failover mock.
func (m *FailoverMockStreamProvider) Models() ([]provider.Model, error) {
	return nil, errFailoverMockNotImplemented
}

// FailoverSteps holds state for failover BDD step definitions.
type FailoverSteps struct {
	registry          *provider.Registry
	health            *failover.HealthManager
	manager           *failover.Manager
	streamHook        *failover.StreamHook
	candidateProvider string
	candidateModel    string
}

// RegisterFailoverSteps wires failover-specific step definitions into the
// godog scenario context.
func RegisterFailoverSteps(ctx *godog.ScenarioContext) {
	fs := &FailoverSteps{}

	ctx.Before(func(bctx context.Context, _ *godog.Scenario) (context.Context, error) {
		fs.registry = nil
		fs.health = nil
		fs.manager = nil
		fs.streamHook = nil
		fs.candidateProvider = ""
		fs.candidateModel = ""
		return bctx, nil
	})

	ctx.Step(`^a failover hook with a single candidate "([^"]*)" / "([^"]*)"$`, fs.aFailoverHookWithSingleCandidate)
	ctx.Step(`^the candidate returns an auth failure error with code "([^"]*)"$`, fs.candidateReturnsAuthFailure)
	ctx.Step(`^the candidate returns a model-not-found error$`, fs.candidateReturnsModelNotFound)
	ctx.Step(`^the failover hook executes a chat request$`, fs.failoverHookExecutesChatRequest)
	ctx.Step(`^the health manager marks "([^"]*)" / "([^"]*)" as rate-limited$`, fs.healthManagerMarksAsRateLimited)
	ctx.Step(`^the health manager does NOT mark "([^"]*)" / "([^"]*)" as rate-limited$`, fs.healthManagerDoesNotMarkAsRateLimited)
	ctx.Step(`^the cooldown for "([^"]*)" / "([^"]*)" is at least (\d+) hours$`, fs.cooldownIsAtLeastHours)
}

// aFailoverHookWithSingleCandidate sets up the failover infrastructure
// (registry, health manager, manager, stream hook) with one provider/model
// candidate. The mock provider itself is registered by a subsequent step.
func (fs *FailoverSteps) aFailoverHookWithSingleCandidate(providerName, model string) error {
	fs.candidateProvider = providerName
	fs.candidateModel = model
	fs.registry = provider.NewRegistry()
	fs.health = failover.NewHealthManager()
	fs.manager = failover.NewManager(fs.registry, fs.health, 2*time.Second)
	fs.streamHook = failover.NewStreamHook(fs.manager, nil, "")
	fs.manager.SetBasePreferences([]provider.ModelPreference{
		{Provider: providerName, Model: model},
	})
	return nil
}

// candidateReturnsAuthFailure registers a mock provider that returns an
// ErrorTypeAuthFailure with the given error code from Stream.
func (fs *FailoverSteps) candidateReturnsAuthFailure(errorCode string) error {
	authErr := &provider.Error{
		HTTPStatus: 401,
		ErrorCode:  errorCode,
		ErrorType:  provider.ErrorTypeAuthFailure,
		Provider:   fs.candidateProvider,
		Message:    "authentication failed",
	}
	fs.registry.Register(&FailoverMockStreamProvider{
		name: fs.candidateProvider,
		streamFn: func(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			return nil, authErr
		},
	})
	return nil
}

// candidateReturnsModelNotFound registers a mock provider that returns an
// ErrorTypeModelNotFound error from Stream.
func (fs *FailoverSteps) candidateReturnsModelNotFound() error {
	modelErr := &provider.Error{
		HTTPStatus: 404,
		ErrorType:  provider.ErrorTypeModelNotFound,
		Provider:   fs.candidateProvider,
		Message:    "model not found",
	}
	fs.registry.Register(&FailoverMockStreamProvider{
		name: fs.candidateProvider,
		streamFn: func(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			return nil, modelErr
		},
	})
	return nil
}

// failoverHookExecutesChatRequest runs the stream hook's Execute handler
// against the configured mock providers. The return values are discarded;
// this step exists to trigger the error classification path so the
// HealthManager state can be asserted in subsequent steps.
func (fs *FailoverSteps) failoverHookExecutesChatRequest() error {
	handler := fs.streamHook.Execute(failoverBaseHandler(fs.registry))
	_, _ = handler(context.Background(), &provider.ChatRequest{})
	return nil
}

// healthManagerMarksAsRateLimited asserts the pair IS marked as rate-limited.
func (fs *FailoverSteps) healthManagerMarksAsRateLimited(providerName, model string) error {
	if !fs.health.IsRateLimited(providerName, model) {
		return fmt.Errorf("expected %q / %q to be rate-limited, but it is not", providerName, model)
	}
	return nil
}

// healthManagerDoesNotMarkAsRateLimited asserts the pair is NOT marked.
func (fs *FailoverSteps) healthManagerDoesNotMarkAsRateLimited(providerName, model string) error {
	if fs.health.IsRateLimited(providerName, model) {
		return fmt.Errorf("expected %q / %q to NOT be rate-limited, but it is", providerName, model)
	}
	return nil
}

// cooldownIsAtLeastHours asserts the remaining cooldown duration meets the
// specified floor.
func (fs *FailoverSteps) cooldownIsAtLeastHours(providerName, model string, minHours int) error {
	until, ok := fs.health.RateLimitedUntil(providerName, model)
	if !ok {
		return fmt.Errorf("expected %q / %q to have a cooldown entry, but none found", providerName, model)
	}
	remaining := time.Until(until)
	minimum := time.Duration(minHours) * time.Hour
	if remaining < minimum {
		return fmt.Errorf("cooldown for %q / %q is %v, expected at least %v", providerName, model, remaining, minimum)
	}
	return nil
}

// failoverBaseHandler creates a hook.HandlerFunc that dispatches to the
// provider registry, mirroring the baseHandler helper in the package-level
// integration tests.
func failoverBaseHandler(registry *provider.Registry) hook.HandlerFunc {
	return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		if req.Provider == "" {
			return nil, fmt.Errorf("no provider specified in request")
		}
		p, err := registry.Get(req.Provider)
		if err != nil {
			return nil, fmt.Errorf("provider %q not found: %w", req.Provider, err)
		}
		return p.Stream(ctx, *req)
	}
}
