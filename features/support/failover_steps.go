//go:build e2e

package support

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	appproviders "github.com/baphled/flowstate/internal/app/providers"
	"github.com/baphled/flowstate/internal/config"
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
	models   []provider.Model
}

// Name returns the provider name.
//
// Returns: result of Name.
// Side effects: None.
//
// Expected: parameters for Name.
func (m *FailoverMockStreamProvider) Name() string { return m.name }

// Stream delegates to the configured streamFn.
//
// Expected: parameters for Stream.
// Returns: result of Stream.
// Side effects: None.
func (m *FailoverMockStreamProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	if m.streamFn == nil {
		return nil, errFailoverMockNotImplemented
	}
	return m.streamFn(ctx, req)
}

// Chat is not implemented in the failover mock.
//
// Expected: parameters for Chat.
// Returns: result of Chat.
// Side effects: None.
func (m *FailoverMockStreamProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, errFailoverMockNotImplemented
}

// Embed is not implemented in the failover mock.
//
// Expected: parameters for Embed.
// Returns: result of Embed.
// Side effects: None.
func (m *FailoverMockStreamProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, errFailoverMockNotImplemented
}

// Models is not implemented in the failover mock.
//
// Returns: result of Models.
// Side effects: None.
//
// Expected: parameters for Models.
func (m *FailoverMockStreamProvider) Models() ([]provider.Model, error) {
	if len(m.models) == 0 {
		return nil, errFailoverMockNotImplemented
	}
	result := make([]provider.Model, len(m.models))
	copy(result, m.models)
	return result, nil
}

// RefreshNow is a no-op that always succeeds, used in S2 reactive refresh tests.
//
// Expected: parameters for RefreshNow.
// Returns: result of RefreshNow.
// Side effects: None.
func (m *FailoverMockStreamProvider) RefreshNow(_ context.Context) error {
	return nil
}

// RefreshStatus returns a recent successful refresh, used in S2 reactive refresh tests.
//
// Returns: result of RefreshStatus.
// Side effects: None.
//
// Expected: parameters for RefreshStatus.
func (m *FailoverMockStreamProvider) RefreshStatus() (time.Time, int) {
	return time.Now(), 0
}

// FailoverSteps holds state for failover BDD step definitions.
type FailoverSteps struct {
	registry          *provider.Registry
	health            *failover.HealthManager
	manager           *failover.Manager
	streamHook        *failover.StreamHook
	candidateProvider string
	candidateModel    string
	defaultProvider   string
	defaultModel      string
	configured        []provider.ModelPreference
	equivalentSpecs   []equivalentProviderSpec
	eligible          map[provider.ModelPreference]bool
	chain             []provider.ModelPreference
	selectionErr      error
	preparedRound     []provider.ModelPreference
	selected          provider.ModelPreference
	selectedModel     string
	rotationOrder     []provider.ModelPreference
	trackedProvider   string
	trackedModel      string
	receivedError     string
	reloadedHealth    *failover.HealthManager
	refreshSucceeded  bool
}

type equivalentProviderSpec struct {
	pref     provider.ModelPreference
	tier     string
	lastUsed int
}

// RegisterFailoverSteps wires failover-specific step definitions into the
// godog scenario context.
//
// Expected: parameters for RegisterFailoverSteps.
// Side effects: None.
func RegisterFailoverSteps(ctx *godog.ScenarioContext) {
	fs := &FailoverSteps{}

	ctx.Before(func(bctx context.Context, _ *godog.Scenario) (context.Context, error) {
		fs.registry = nil
		fs.health = nil
		fs.manager = nil
		fs.streamHook = nil
		fs.candidateProvider = ""
		fs.candidateModel = ""
		fs.defaultProvider = ""
		fs.defaultModel = ""
		fs.configured = nil
		fs.equivalentSpecs = nil
		fs.eligible = nil
		fs.chain = nil
		fs.selectionErr = nil
		fs.preparedRound = nil
		fs.selected = provider.ModelPreference{}
		fs.selectedModel = ""
		fs.rotationOrder = nil
		fs.trackedProvider = ""
		fs.trackedModel = ""
		fs.receivedError = ""
		fs.reloadedHealth = nil
		fs.refreshSucceeded = false
		return bctx, nil
	})

	ctx.Step(`^a failover hook with a single candidate "([^"]*)" / "([^"]*)"$`, fs.aFailoverHookWithSingleCandidate)
	ctx.Step(`^the configured providers are:$`, fs.theConfiguredProvidersAre)
	ctx.Step(`^providers\.default is "([^"]*)"$`, fs.providersDefaultIs)
	ctx.Step(`^the failover chain is built$`, fs.theFailoverChainIsBuilt)
	ctx.Step(`^the candidate order should be:$`, fs.theCandidateOrderShouldBe)
	ctx.Step(`^the provider "([^"]*)" / "([^"]*)" should not be present$`, fs.theProviderShouldNotBePresent)
	ctx.Step(`^the default provider validation should fail with "([^"]*)"$`, fs.theDefaultProviderValidationShouldFailWith)
	ctx.Step(`^the chain should be empty$`, fs.theChainShouldBeEmpty)
	ctx.Step(`^the health manager tracks "([^"]*)" / "([^"]*)"$`, fs.theHealthManagerTracks)
	ctx.Step(`^the pair receives "([^"]*)"$`, fs.thePairReceives)
	ctx.Step(`^the failover hook classifies the error$`, fs.theFailoverHookClassifiesTheError)
	ctx.Step(`^the refresh outcome should be remembered as successful$`, fs.theRefreshOutcomeShouldBeRememberedAsSuccessful)
	ctx.Step(`^the health manager restarts$`, fs.theHealthManagerRestarts)
	ctx.Step(`^the pair should be in cooldown$`, fs.thePairShouldBeInCooldown)
	ctx.Step(`^the pair should not be in cooldown$`, fs.thePairShouldNotBeInCooldown)
	ctx.Step(`^the cooldown should survive a restart$`, fs.theCooldownShouldSurviveARestart)
	ctx.Step(`^the cooldown should be at least (\d+) hour[s]?$`, fs.theCooldownShouldBeAtLeastHours)
	ctx.Step(`^the error should be reported as request-correctable$`, fs.theErrorShouldBeReportedAsRequestCorrectable)
	ctx.Step(`^the following healthy candidates are configured:$`, fs.theFollowingHealthyCandidatesAreConfigured)
	ctx.Step(`^the next round candidates are prepared$`, fs.theNextRoundCandidatesArePrepared)
	ctx.Step(`^the selected provider should be "([^"]*)" / "([^"]*)"$`, fs.theSelectedProviderShouldBe)
	ctx.Step(`^the candidate order should stay as configured$`, fs.theCandidateOrderShouldStayAsConfigured)
	ctx.Step(`^the selection should remain stable between attempts$`, fs.theSelectionShouldRemainStableBetweenAttempts)
	ctx.Step(`^([^"]*) becomes cooldowned before its attempt$`, fs.providerBecomesCooldownedBeforeItsAttempt)
	ctx.Step(`^the leading candidate should be skipped before its attempt$`, fs.theLeadingCandidateShouldBeSkippedBeforeItsAttempt)
	ctx.Step(`^every candidate is already in cooldown$`, fs.everyCandidateIsAlreadyInCooldown)
	ctx.Step(`^the selection should fail with "([^"]*)"$`, fs.theSelectionShouldFailWith)
	ctx.Step(`^no attempt should be made$`, fs.noAttemptShouldBeMade)
	ctx.Step(`^the following equivalent providers are configured:$`, fs.theFollowingEquivalentProvidersAreConfigured)
	ctx.Step(`^the rotation order is calculated$`, fs.theRotationOrderIsCalculated)
	ctx.Step(`^the rotation order should be:$`, fs.theRotationOrderShouldBe)
	ctx.Step(`^the rotation should be based on least recently used$`, fs.theRotationShouldBeBasedOnLeastRecentlyUsed)
	ctx.Step(`^providers in different tiers should not rotate across the tier boundary$`, fs.providersInDifferentTiersShouldNotRotateAcrossTheTierBoundary)
	ctx.Step(`^the single-provider tier ordering should stay unchanged$`, fs.theSingleProviderTierOrderingShouldStayUnchanged)
	ctx.Step(`^the candidate returns an auth failure error with code "([^"]*)"$`, fs.candidateReturnsAuthFailure)
	ctx.Step(`^the candidate fails once with auth failure then succeeds$`, fs.candidateFailsOnceThenSucceeds)
	ctx.Step(`^the candidate returns a model-not-found error$`, fs.candidateReturnsModelNotFound)
	ctx.Step(`^the failover hook executes a chat request$`, fs.failoverHookExecutesChatRequest)
	ctx.Step(`^the health manager marks "([^"]*)" / "([^"]*)" as rate-limited$`, fs.healthManagerMarksAsRateLimited)
	ctx.Step(`^the health manager does NOT mark "([^"]*)" / "([^"]*)" as rate-limited$`, fs.healthManagerDoesNotMarkAsRateLimited)
	ctx.Step(`^the cooldown for "([^"]*)" / "([^"]*)" is at least (\d+) hours$`, fs.cooldownIsAtLeastHours)
}

// aFailoverHookWithSingleCandidate sets up the failover infrastructure
// (registry, health manager, manager, stream hook) with one provider/model
// candidate. The mock provider itself is registered by a subsequent step.
//
// Expected: parameters for aFailoverHookWithSingleCandidate.
// Returns: result of aFailoverHookWithSingleCandidate.
// Side effects: None.
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
//
// Expected: parameters for candidateReturnsAuthFailure.
// Returns: result of candidateReturnsAuthFailure.
// Side effects: None.
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

// candidateFailsOnceThenSucceeds registers a mock provider that returns an
// ErrorTypeAuthFailure on the first Stream call, then succeeds on
// every subsequent call. Simulates the S2 refresh-retry flow where
// the hook refreshes the token and retries the request.
//
// Returns: result of candidateFailsOnceThenSucceeds.
// Side effects: None.
//
// Expected: parameters for candidateFailsOnceThenSucceeds.
func (fs *FailoverSteps) candidateFailsOnceThenSucceeds() error {
	var callCount int
	fs.registry.Register(&FailoverMockStreamProvider{
		name: fs.candidateProvider,
		streamFn: func(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			callCount++
			if callCount == 1 {
				return nil, &provider.Error{
					HTTPStatus: 401,
					ErrorCode:  "token_expired",
					ErrorType:  provider.ErrorTypeAuthFailure,
					Provider:   fs.candidateProvider,
					Message:    "token expired",
				}
			}
			ch := make(chan provider.StreamChunk, 2)
			ch <- provider.StreamChunk{Content: "Hello", Done: false}
			ch <- provider.StreamChunk{Done: true}
			close(ch)
			return ch, nil
		},
	})
	return nil
}

// candidateReturnsModelNotFound registers a mock provider that returns an
// ErrorTypeModelNotFound error from Stream.
//
// Returns: result of candidateReturnsModelNotFound.
// Side effects: None.
//
// Expected: parameters for candidateReturnsModelNotFound.
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
//
// Returns: result of failoverHookExecutesChatRequest.
// Side effects: None.
//
// Expected: parameters for failoverHookExecutesChatRequest.
func (fs *FailoverSteps) failoverHookExecutesChatRequest() error {
	handler := fs.streamHook.Execute(failoverBaseHandler(fs.registry))
	_, _ = handler(context.Background(), &provider.ChatRequest{})
	return nil
}

// healthManagerMarksAsRateLimited asserts the pair IS marked as rate-limited.
//
// Expected: parameters for healthManagerMarksAsRateLimited.
// Returns: result of healthManagerMarksAsRateLimited.
// Side effects: None.
func (fs *FailoverSteps) healthManagerMarksAsRateLimited(providerName, model string) error {
	if !fs.health.IsRateLimited(providerName, model) {
		return fmt.Errorf("expected %q / %q to be rate-limited, but it is not", providerName, model)
	}
	return nil
}

// healthManagerDoesNotMarkAsRateLimited asserts the pair is NOT marked.
//
// Expected: parameters for healthManagerDoesNotMarkAsRateLimited.
// Returns: result of healthManagerDoesNotMarkAsRateLimited.
// Side effects: None.
func (fs *FailoverSteps) healthManagerDoesNotMarkAsRateLimited(providerName, model string) error {
	if fs.health.IsRateLimited(providerName, model) {
		return fmt.Errorf("expected %q / %q to NOT be rate-limited, but it is", providerName, model)
	}
	return nil
}

// cooldownIsAtLeastHours asserts the remaining cooldown duration meets the
// specified floor.
//
// Expected: parameters for cooldownIsAtLeastHours.
// Returns: result of cooldownIsAtLeastHours.
// Side effects: None.
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
//
// Expected: parameters for failoverBaseHandler.
// Returns: result of failoverBaseHandler.
// Side effects: None.
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

// ensureFailoverState ...
//
// Side effects: None.
//
// Expected: parameters for ensureFailoverState.
// Returns: result of ensureFailoverState.
func (fs *FailoverSteps) ensureFailoverState() {
	if fs.registry == nil {
		fs.registry = provider.NewRegistry()
	}
	if fs.health == nil {
		fs.health = failover.NewHealthManager()
	}
	if fs.manager == nil {
		fs.manager = failover.NewManager(fs.registry, fs.health, 2*time.Second)
	}
	if fs.streamHook == nil {
		fs.streamHook = failover.NewStreamHook(fs.manager, nil, "")
	}
}

// parseTruthy ...
//
// Expected: parameters for parseTruthy.
//
// Returns: result of parseTruthy.
//
// Side effects: None.
func parseTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "present", "configured", "eligible", "host+model", "effective":
		return true
	default:
		return false
	}
}

// parsePreferenceRows ...
//
// Expected: parameters for parsePreferenceRows.
//
// Returns: result of parsePreferenceRows.
//
// Side effects: None.
func parsePreferenceRows(table *godog.Table, providerIdx, modelIdx int) []provider.ModelPreference {
	prefs := make([]provider.ModelPreference, 0, len(table.Rows)-1)
	for i, row := range table.Rows {
		if i == 0 {
			continue
		}
		prefs = append(prefs, provider.ModelPreference{Provider: row.Cells[providerIdx].Value, Model: row.Cells[modelIdx].Value})
	}
	return prefs
}

// parseExpectedPairs ...
//
// Expected: parameters for parseExpectedPairs.
//
// Returns: result of parseExpectedPairs.
//
// Side effects: None.
func parseExpectedPairs(table *godog.Table) []provider.ModelPreference {
	return parsePreferenceRows(table, 0, 1)
}

// parseScore ...
//
// Expected: parameters for parseScore.
//
// Returns: result of parseScore.
//
// Side effects: None.
func parseScore(value string) int {
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

// configuredModel ...
//
// Expected: parameters for configuredModel.
//
// Returns: result of configuredModel.
//
// Side effects: None.
func (fs *FailoverSteps) configuredModel(providerName string) (provider.ModelPreference, bool) {
	for _, pref := range fs.configured {
		if pref.Provider == providerName {
			return pref, true
		}
	}
	return provider.ModelPreference{}, false
}

// theConfiguredProvidersAre ...
//
// Expected: parameters for theConfiguredProvidersAre.
//
// Returns: result of theConfiguredProvidersAre.
//
// Side effects: None.
func (fs *FailoverSteps) theConfiguredProvidersAre(table *godog.Table) error {
	fs.configured = nil
	fs.eligible = make(map[provider.ModelPreference]bool)

	for i, row := range table.Rows {
		if i == 0 {
			continue
		}
		pref := provider.ModelPreference{Provider: row.Cells[0].Value, Model: row.Cells[1].Value}
		eligible := parseTruthy(row.Cells[len(row.Cells)-1].Value)
		if len(row.Cells) > 2 {
			eligible = parseTruthy(row.Cells[2].Value)
		}
		fs.configured = append(fs.configured, pref)
		fs.eligible[pref] = eligible
	}

	return nil
}

// providersDefaultIs ...
//
// Expected: parameters for providersDefaultIs.
//
// Returns: result of providersDefaultIs.
//
// Side effects: None.
func (fs *FailoverSteps) providersDefaultIs(providerName string) error {
	fs.defaultProvider = providerName
	return nil
}

// theFailoverChainIsBuilt ...
//
// Returns: result of theFailoverChainIsBuilt.
//
// Side effects: None.
//
// Expected: parameters for theFailoverChainIsBuilt.
func (fs *FailoverSteps) theFailoverChainIsBuilt() error {
	fs.selectionErr = nil
	fs.chain = nil
	fs.selected = provider.ModelPreference{}
	fs.selectedModel = ""

	return withClearedProviderEnv(func() error {
		cfg, err := fs.loadConfigDrivenConfig()
		if err != nil {
			fs.selectionErr = err
			return nil
		}

		fs.chain = appproviders.BuildConfigPreferences(cfg)
		if len(fs.chain) > 0 {
			fs.selected = fs.chain[0]
			fs.selectedModel = fs.selected.Model
		}
		return nil
	})
}

// theCandidateOrderShouldBe ...
//
// Expected: parameters for theCandidateOrderShouldBe.
//
// Returns: result of theCandidateOrderShouldBe.
//
// Side effects: None.
func (fs *FailoverSteps) theCandidateOrderShouldBe(table *godog.Table) error {
	expected := parseExpectedPairs(table)
	actual := fs.chain
	if actual == nil {
		if fs.manager == nil {
			return errors.New("failover manager not initialised")
		}
		actual = fs.manager.Candidates()
	}
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("expected candidate order %v, got %v", expected, actual)
	}
	return nil
}

// theProviderShouldNotBePresent ...
//
// Expected: parameters for theProviderShouldNotBePresent.
//
// Returns: result of theProviderShouldNotBePresent.
//
// Side effects: None.
func (fs *FailoverSteps) theProviderShouldNotBePresent(providerName, model string) error {
	actual := fs.chain
	if actual == nil && fs.manager != nil {
		actual = fs.manager.Candidates()
	}
	for _, pref := range actual {
		if pref.Provider == providerName && pref.Model == model {
			return fmt.Errorf("did not expect %q / %q to be present in %v", providerName, model, actual)
		}
	}
	return nil
}

// theDefaultProviderValidationShouldFailWith ...
//
// Expected: parameters for theDefaultProviderValidationShouldFailWith.
//
// Returns: result of theDefaultProviderValidationShouldFailWith.
//
// Side effects: None.
func (fs *FailoverSteps) theDefaultProviderValidationShouldFailWith(expected string) error {
	if fs.selectionErr == nil {
		return fmt.Errorf("expected an error containing %q, got nil", expected)
	}
	if !strings.Contains(strings.ToLower(fs.selectionErr.Error()), strings.ToLower(expected)) {
		return fmt.Errorf("expected error containing %q, got %q", expected, fs.selectionErr.Error())
	}
	return nil
}

// theChainShouldBeEmpty ...
//
// Returns: result of theChainShouldBeEmpty.
//
// Side effects: None.
//
// Expected: parameters for theChainShouldBeEmpty.
func (fs *FailoverSteps) theChainShouldBeEmpty() error {
	actual := fs.chain
	if actual == nil && fs.manager != nil {
		actual = fs.manager.Candidates()
	}
	if len(actual) != 0 {
		return fmt.Errorf("expected an empty chain, got %v", actual)
	}
	return nil
}

// theHealthManagerTracks ...
//
// Expected: parameters for theHealthManagerTracks.
//
// Returns: result of theHealthManagerTracks.
//
// Side effects: None.
func (fs *FailoverSteps) theHealthManagerTracks(providerName, model string) error {
	fs.ensureFailoverState()
	if fs.health == nil {
		return errors.New("health manager not initialised")
	}
	persistDir, err := os.MkdirTemp("", "failover-health-classification-*")
	if err != nil {
		return err
	}
	fs.health.SetPersistPath(filepath.Join(persistDir, "provider-health.json"))
	fs.trackedProvider = providerName
	fs.trackedModel = model
	return nil
}

// thePairReceives ...
//
// Expected: parameters for thePairReceives.
//
// Returns: result of thePairReceives.
//
// Side effects: None.
func (fs *FailoverSteps) thePairReceives(errorText string) error {
	fs.receivedError = errorText
	return nil
}

// theFailoverHookClassifiesTheError ...
//
// Returns: result of theFailoverHookClassifiesTheError.
//
// Side effects: None.
//
// Expected: parameters for theFailoverHookClassifiesTheError.
func (fs *FailoverSteps) theFailoverHookClassifiesTheError() error {
	fs.ensureFailoverState()
	fs.refreshSucceeded = false
	fs.selectionErr = nil
	if fs.health == nil {
		return errors.New("health manager not initialised")
	}
	if fs.registry == nil {
		fs.registry = provider.NewRegistry()
	}
	if fs.trackedProvider == "" || fs.trackedModel == "" {
		return errors.New("health classification target not initialised")
	}
	fs.registry.Register(&FailoverMockStreamProvider{
		name:     fs.trackedProvider,
		streamFn: fs.classificationStreamFn(),
	})
	fs.manager.SetBasePreferences([]provider.ModelPreference{{Provider: fs.trackedProvider, Model: fs.trackedModel}})
	handler := fs.streamHook.Execute(failoverBaseHandler(fs.registry))
	ch, err := handler(context.Background(), &provider.ChatRequest{})
	if ch != nil {
		for range ch {
		}
	}
	if err != nil {
		if fs.requestCorrectableScenario() {
			fs.selectionErr = fmt.Errorf("request-correctable: %w", err)
		} else {
			fs.selectionErr = err
		}
	}
	if strings.Contains(strings.ToLower(fs.receivedError), "successful refresh") && !fs.health.IsRateLimited(fs.trackedProvider, fs.trackedModel) {
		fs.refreshSucceeded = true
	}
	return nil
}

// theRefreshOutcomeShouldBeRememberedAsSuccessful ...
//
// Returns: result of theRefreshOutcomeShouldBeRememberedAsSuccessful.
//
// Side effects: None.
//
// Expected: parameters for theRefreshOutcomeShouldBeRememberedAsSuccessful.
func (fs *FailoverSteps) theRefreshOutcomeShouldBeRememberedAsSuccessful() error {
	if !fs.refreshSucceeded {
		return errors.New("expected refresh outcome to be remembered as successful")
	}
	return nil
}

// theHealthManagerRestarts ...
//
// Returns: result of theHealthManagerRestarts.
//
// Side effects: None.
//
// Expected: parameters for theHealthManagerRestarts.
func (fs *FailoverSteps) theHealthManagerRestarts() error {
	if fs.health == nil {
		return errors.New("health manager not initialised")
	}
	fs.reloadedHealth = failover.NewHealthManager()
	fs.reloadedHealth.SetPersistPath(fs.health.PersistPath())
	if err := fs.reloadedHealth.LoadState(fs.health.PersistPath()); err != nil {
		return err
	}
	return nil
}

// thePairShouldBeInCooldown ...
//
// Returns: result of thePairShouldBeInCooldown.
//
// Side effects: None.
//
// Expected: parameters for thePairShouldBeInCooldown.
func (fs *FailoverSteps) thePairShouldBeInCooldown() error {
	if fs.health == nil {
		return errors.New("health manager not initialised")
	}
	if !fs.health.IsRateLimited(fs.trackedProvider, fs.trackedModel) {
		return fmt.Errorf("expected %q / %q to be in cooldown after %q", fs.trackedProvider, fs.trackedModel, fs.receivedError)
	}
	return nil
}

// thePairShouldNotBeInCooldown ...
//
// Returns: result of thePairShouldNotBeInCooldown.
//
// Side effects: None.
//
// Expected: parameters for thePairShouldNotBeInCooldown.
func (fs *FailoverSteps) thePairShouldNotBeInCooldown() error {
	if fs.health == nil {
		return errors.New("health manager not initialised")
	}
	if fs.health.IsRateLimited(fs.trackedProvider, fs.trackedModel) {
		return fmt.Errorf("expected %q / %q to remain healthy", fs.trackedProvider, fs.trackedModel)
	}
	return nil
}

// theCooldownShouldSurviveARestart ...
//
// Returns: result of theCooldownShouldSurviveARestart.
//
// Side effects: None.
//
// Expected: parameters for theCooldownShouldSurviveARestart.
func (fs *FailoverSteps) theCooldownShouldSurviveARestart() error {
	if fs.reloadedHealth == nil {
		fs.reloadedHealth = failover.NewHealthManager()
	}
	if !fs.reloadedHealth.IsRateLimited(fs.trackedProvider, fs.trackedModel) {
		return fmt.Errorf("expected cooldown for %q / %q to survive restart", fs.trackedProvider, fs.trackedModel)
	}
	return nil
}

// theCooldownShouldBeAtLeastHours ...
//
// Expected: parameters for theCooldownShouldBeAtLeastHours.
//
// Returns: result of theCooldownShouldBeAtLeastHours.
//
// Side effects: None.
func (fs *FailoverSteps) theCooldownShouldBeAtLeastHours(minHours int) error {
	if fs.health == nil {
		return errors.New("health manager not initialised")
	}
	until, ok := fs.health.RateLimitedUntil(fs.trackedProvider, fs.trackedModel)
	if !ok {
		return fmt.Errorf("expected %q / %q to have a cooldown", fs.trackedProvider, fs.trackedModel)
	}
	remaining := time.Until(until)
	if remaining < time.Duration(minHours)*time.Hour {
		return fmt.Errorf("cooldown for %q / %q is %v, expected at least %d hours", fs.trackedProvider, fs.trackedModel, remaining, minHours)
	}
	return nil
}

// theErrorShouldBeReportedAsRequestCorrectable ...
//
// Returns: result of theErrorShouldBeReportedAsRequestCorrectable.
//
// Side effects: None.
//
// Expected: parameters for theErrorShouldBeReportedAsRequestCorrectable.
func (fs *FailoverSteps) theErrorShouldBeReportedAsRequestCorrectable() error {
	if fs.selectionErr == nil {
		return errors.New("request-correctable error classification not recorded")
	}
	if !strings.Contains(strings.ToLower(fs.selectionErr.Error()), "request-correctable") {
		return fmt.Errorf("expected request-correctable classification, got %q", fs.selectionErr.Error())
	}
	return nil
}

// requestCorrectableScenario ...
//
// Returns: result of requestCorrectableScenario.
//
// Side effects: None.
//
// Expected: parameters for requestCorrectableScenario.
func (fs *FailoverSteps) requestCorrectableScenario() bool {
	lower := strings.ToLower(fs.receivedError)
	return strings.Contains(lower, "context window exceeded") || strings.Contains(lower, "malformed request")
}

// classificationStreamFn ...
//
// Returns: result of classificationStreamFn.
//
// Side effects: None.
//
// Expected: parameters for classificationStreamFn.
func (fs *FailoverSteps) classificationStreamFn() func(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	lower := strings.ToLower(fs.receivedError)
	switch {
	case strings.Contains(lower, "successful refresh"):
		calls := 0
		return func(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			calls++
			if calls == 1 {
				return nil, &provider.Error{
					HTTPStatus: 401,
					ErrorCode:  "token_expired",
					ErrorType:  provider.ErrorTypeAuthFailure,
					Provider:   fs.trackedProvider,
					Message:    "token expired",
				}
			}
			ch := make(chan provider.StreamChunk, 1)
			ch <- provider.StreamChunk{Content: "refresh succeeded", Done: true}
			close(ch)
			return ch, nil
		}
	case strings.Contains(lower, "failed refresh"):
		calls := 0
		return func(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			calls++
			if calls <= 2 {
				return nil, &provider.Error{
					HTTPStatus: 401,
					ErrorCode:  "token_expired",
					ErrorType:  provider.ErrorTypeAuthFailure,
					Provider:   fs.trackedProvider,
					Message:    "token expired",
				}
			}
			ch := make(chan provider.StreamChunk, 1)
			ch <- provider.StreamChunk{Content: "retry failed", Done: true}
			close(ch)
			return ch, nil
		}
	case strings.Contains(lower, "context window exceeded"):
		return func(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			return nil, &provider.Error{
				ErrorType: provider.ErrorTypeContextWindowExceeded,
				Provider:  fs.trackedProvider,
				Message:   "prompt is too long for context window",
			}
		}
	default:
		return func(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			return nil, errors.New(fs.receivedError)
		}
	}
}

// theFollowingHealthyCandidatesAreConfigured ...
//
// Expected: parameters for theFollowingHealthyCandidatesAreConfigured.
//
// Returns: result of theFollowingHealthyCandidatesAreConfigured.
//
// Side effects: None.
func (fs *FailoverSteps) theFollowingHealthyCandidatesAreConfigured(table *godog.Table) error {
	fs.ensureFailoverState()
	fs.configured = nil
	fs.eligible = nil
	fs.selectionErr = nil
	fs.preparedRound = nil
	fs.selected = provider.ModelPreference{}
	fs.selectedModel = ""
	tempDir, err := os.MkdirTemp("", "failover-health-selection-*")
	if err != nil {
		return err
	}
	fs.health.SetPersistPath(filepath.Join(tempDir, "provider-health.json"))

	for i, row := range table.Rows {
		if i == 0 {
			continue
		}
		pref := provider.ModelPreference{Provider: row.Cells[0].Value, Model: row.Cells[1].Value}
		score := 0
		if len(row.Cells) > 2 {
			score = parseScore(row.Cells[2].Value)
		}
		fs.configured = append(fs.configured, pref)
		fs.registry.Register(&FailoverMockStreamProvider{name: pref.Provider, models: []provider.Model{{ID: pref.Model, Provider: pref.Provider}}})
		fs.seedFailureHistory(pref, score)
	}

	fs.manager.SetBasePreferences(fs.configured)
	return nil
}

// theNextRoundCandidatesArePrepared ...
//
// Returns: result of theNextRoundCandidatesArePrepared.
//
// Side effects: None.
//
// Expected: parameters for theNextRoundCandidatesArePrepared.
func (fs *FailoverSteps) theNextRoundCandidatesArePrepared() error {
	if fs.manager == nil {
		return errors.New("failover manager not initialised")
	}
	fs.preparedRound = fs.healthyConfiguredCandidates()
	selected, ok := fs.selectBestHealthyConfiguredCandidate(fs.preparedRound)
	if !ok {
		fs.selectionErr = errors.New("no healthy providers available")
		fs.preparedRound = nil
		return nil
	}
	fs.selected = selected
	fs.selectedModel = selected.Model
	return nil
}

// theSelectedProviderShouldBe ...
//
// Expected: parameters for theSelectedProviderShouldBe.
//
// Returns: result of theSelectedProviderShouldBe.
//
// Side effects: None.
func (fs *FailoverSteps) theSelectedProviderShouldBe(providerName, model string) error {
	actual := fs.selected
	if actual.Provider == "" && actual.Model == "" && len(fs.preparedRound) > 0 {
		actual = fs.preparedRound[0]
	}
	if actual.Provider != providerName || actual.Model != model {
		return fmt.Errorf("expected selected provider %q / %q, got %q / %q", providerName, model, actual.Provider, actual.Model)
	}
	return nil
}

// theCandidateOrderShouldStayAsConfigured ...
//
// Returns: result of theCandidateOrderShouldStayAsConfigured.
//
// Side effects: None.
//
// Expected: parameters for theCandidateOrderShouldStayAsConfigured.
func (fs *FailoverSteps) theCandidateOrderShouldStayAsConfigured() error {
	if !reflect.DeepEqual(fs.preparedRound, fs.configured) {
		return fmt.Errorf("expected order to stay as configured, got %v", fs.preparedRound)
	}
	return nil
}

// theSelectionShouldRemainStableBetweenAttempts ...
//
// Returns: result of theSelectionShouldRemainStableBetweenAttempts.
//
// Side effects: None.
//
// Expected: parameters for theSelectionShouldRemainStableBetweenAttempts.
func (fs *FailoverSteps) theSelectionShouldRemainStableBetweenAttempts() error {
	if fs.manager == nil {
		return errors.New("failover manager not initialised")
	}
	again, ok := fs.selectBestHealthyConfiguredCandidate(fs.healthyConfiguredCandidates())
	if !ok {
		return errors.New("no healthy providers available")
	}
	if again != fs.selected {
		return fmt.Errorf("expected stable selection %v / %v, got %v / %v", fs.selected.Provider, fs.selected.Model, again.Provider, again.Model)
	}
	return nil
}

// providerBecomesCooldownedBeforeItsAttempt ...
//
// Expected: parameters for providerBecomesCooldownedBeforeItsAttempt.
//
// Returns: result of providerBecomesCooldownedBeforeItsAttempt.
//
// Side effects: None.
func (fs *FailoverSteps) providerBecomesCooldownedBeforeItsAttempt(providerName string) error {
	if fs.health == nil {
		return errors.New("health manager not initialised")
	}
	pref, ok := fs.configuredModel(providerName)
	if !ok {
		return fmt.Errorf("provider %q is not configured", providerName)
	}
	fs.health.MarkRateLimited(pref.Provider, pref.Model, time.Now().Add(time.Hour))
	fs.trackedProvider = pref.Provider
	fs.trackedModel = pref.Model
	return nil
}

// theLeadingCandidateShouldBeSkippedBeforeItsAttempt ...
//
// Returns: result of theLeadingCandidateShouldBeSkippedBeforeItsAttempt.
//
// Side effects: None.
//
// Expected: parameters for theLeadingCandidateShouldBeSkippedBeforeItsAttempt.
func (fs *FailoverSteps) theLeadingCandidateShouldBeSkippedBeforeItsAttempt() error {
	if fs.manager == nil {
		return errors.New("failover manager not initialised")
	}
	fresh := fs.healthyConfiguredCandidates()
	if len(fresh) == 0 {
		return errors.New("no healthy providers available")
	}
	selected, ok := fs.selectBestHealthyConfiguredCandidate(fresh)
	if !ok {
		return errors.New("no healthy providers available")
	}
	if selected.Provider == fs.trackedProvider && selected.Model == fs.trackedModel {
		return fmt.Errorf("expected %q / %q to be skipped before its attempt, but it remains first", fs.trackedProvider, fs.trackedModel)
	}
	fs.preparedRound = fresh
	fs.selected = selected
	fs.selectedModel = selected.Model
	return nil
}

// everyCandidateIsAlreadyInCooldown ...
//
// Returns: result of everyCandidateIsAlreadyInCooldown.
//
// Side effects: None.
//
// Expected: parameters for everyCandidateIsAlreadyInCooldown.
func (fs *FailoverSteps) everyCandidateIsAlreadyInCooldown() error {
	if fs.health == nil {
		return errors.New("health manager not initialised")
	}
	for _, pref := range fs.configured {
		fs.health.MarkRateLimited(pref.Provider, pref.Model, time.Now().Add(time.Hour))
	}
	return nil
}

// theSelectionShouldFailWith ...
//
// Expected: parameters for theSelectionShouldFailWith.
//
// Returns: result of theSelectionShouldFailWith.
//
// Side effects: None.
func (fs *FailoverSteps) theSelectionShouldFailWith(expected string) error {
	if fs.selectionErr == nil {
		return fmt.Errorf("expected an error containing %q, got nil", expected)
	}
	if !strings.Contains(strings.ToLower(fs.selectionErr.Error()), strings.ToLower(expected)) {
		return fmt.Errorf("expected an error containing %q, got %q", expected, fs.selectionErr.Error())
	}
	return nil
}

// noAttemptShouldBeMade ...
//
// Returns: result of noAttemptShouldBeMade.
//
// Side effects: None.
//
// Expected: parameters for noAttemptShouldBeMade.
func (fs *FailoverSteps) noAttemptShouldBeMade() error {
	if len(fs.preparedRound) != 0 {
		return fmt.Errorf("expected no attempt to be made, but prepared candidates were %v", fs.preparedRound)
	}
	return nil
}

// seedFailureHistory ...
//
// Expected: parameters for seedFailureHistory.
//
// Side effects: None.
//
// Returns: result of seedFailureHistory.
func (fs *FailoverSteps) seedFailureHistory(pref provider.ModelPreference, score int) {
	if score <= 0 {
		return
	}
	for range score {
		fs.health.MarkRateLimited(pref.Provider, pref.Model, time.Now().Add(time.Hour))
	}
	fs.health.MarkRateLimited(pref.Provider, pref.Model, time.Now().Add(-time.Minute))
}

// healthyConfiguredCandidates ...
//
// Returns: result of healthyConfiguredCandidates.
//
// Side effects: None.
//
// Expected: parameters for healthyConfiguredCandidates.
func (fs *FailoverSteps) healthyConfiguredCandidates() []provider.ModelPreference {
	if fs.health == nil {
		return nil
	}
	result := make([]provider.ModelPreference, 0, len(fs.configured))
	for _, pref := range fs.configured {
		if fs.health.IsRateLimited(pref.Provider, pref.Model) {
			continue
		}
		result = append(result, pref)
	}
	return result
}

// selectBestHealthyConfiguredCandidate ...
//
// Expected: parameters for selectBestHealthyConfiguredCandidate.
//
// Returns: result of selectBestHealthyConfiguredCandidate.
//
// Side effects: None.
func (fs *FailoverSteps) selectBestHealthyConfiguredCandidate(candidates []provider.ModelPreference) (provider.ModelPreference, bool) {
	if len(candidates) == 0 || fs.health == nil {
		return provider.ModelPreference{}, false
	}
	now := time.Now()
	bestIdx := -1
	var bestScore uint64
	for i, candidate := range candidates {
		score := fs.health.HealthScore(candidate.Provider, candidate.Model, now)
		if bestIdx == -1 || score < bestScore {
			bestIdx = i
			bestScore = score
		}
	}
	if bestIdx == -1 {
		return provider.ModelPreference{}, false
	}
	return candidates[bestIdx], true
}

// theFollowingEquivalentProvidersAreConfigured ...
//
// Expected: parameters for theFollowingEquivalentProvidersAreConfigured.
//
// Returns: result of theFollowingEquivalentProvidersAreConfigured.
//
// Side effects: None.
func (fs *FailoverSteps) theFollowingEquivalentProvidersAreConfigured(table *godog.Table) error {
	fs.ensureFailoverState()
	fs.configured = nil
	fs.equivalentSpecs = nil
	fs.rotationOrder = nil
	modelTiers := make(map[string]string, len(table.Rows))

	for i, row := range table.Rows {
		if i == 0 {
			continue
		}
		pref := provider.ModelPreference{Provider: row.Cells[0].Value, Model: row.Cells[1].Value}
		lastUsed := parseScore(row.Cells[3].Value)
		tier := row.Cells[2].Value
		fs.configured = append(fs.configured, pref)
		fs.equivalentSpecs = append(fs.equivalentSpecs, equivalentProviderSpec{pref: pref, tier: tier, lastUsed: lastUsed})
		modelTiers[pref.Model] = tier
		fs.registry.Register(&FailoverMockStreamProvider{name: pref.Provider, models: []provider.Model{{ID: pref.Model, Provider: pref.Provider}}})
	}

	fs.manager.SetModelTiers(modelTiers)
	fs.manager.SetBasePreferences(fs.configured)
	fs.seedEquivalentAttempts()
	return nil
}

// theRotationOrderIsCalculated ...
//
// Returns: result of theRotationOrderIsCalculated.
//
// Side effects: None.
//
// Expected: parameters for theRotationOrderIsCalculated.
func (fs *FailoverSteps) theRotationOrderIsCalculated() error {
	if fs.manager == nil {
		return errors.New("failover manager not initialised")
	}
	fs.rotationOrder = fs.manager.Candidates()
	return nil
}

// theRotationOrderShouldBe ...
//
// Expected: parameters for theRotationOrderShouldBe.
//
// Returns: result of theRotationOrderShouldBe.
//
// Side effects: None.
func (fs *FailoverSteps) theRotationOrderShouldBe(table *godog.Table) error {
	expected := parseExpectedPairs(table)
	if !reflect.DeepEqual(fs.rotationOrder, expected) {
		return fmt.Errorf("expected rotation order %v, got %v", expected, fs.rotationOrder)
	}
	return nil
}

// theRotationShouldBeBasedOnLeastRecentlyUsed ...
//
// Returns: result of theRotationShouldBeBasedOnLeastRecentlyUsed.
//
// Side effects: None.
//
// Expected: parameters for theRotationShouldBeBasedOnLeastRecentlyUsed.
func (fs *FailoverSteps) theRotationShouldBeBasedOnLeastRecentlyUsed() error {
	if len(fs.equivalentSpecs) == 0 {
		return errors.New("equivalent providers not initialised")
	}
	expected := fs.rotationOrderByLeastRecentlyUsed()
	if !reflect.DeepEqual(fs.rotationOrder, expected) {
		return fmt.Errorf("expected least-recently-used order %v, got %v", expected, fs.rotationOrder)
	}
	return nil
}

// providersInDifferentTiersShouldNotRotateAcrossTheTierBoundary ...
//
// Returns: result of providersInDifferentTiersShouldNotRotateAcrossTheTierBoundary.
//
// Side effects: None.
//
// Expected: parameters for providersInDifferentTiersShouldNotRotateAcrossTheTierBoundary.
func (fs *FailoverSteps) providersInDifferentTiersShouldNotRotateAcrossTheTierBoundary() error {
	if len(fs.equivalentSpecs) < 2 {
		return errors.New("equivalent providers not initialised")
	}
	if !reflect.DeepEqual(fs.rotationOrder, fs.configured) {
		return fmt.Errorf("expected different tiers to keep the configured order, got %v", fs.rotationOrder)
	}
	return nil
}

// theSingleProviderTierOrderingShouldStayUnchanged ...
//
// Returns: result of theSingleProviderTierOrderingShouldStayUnchanged.
//
// Side effects: None.
//
// Expected: parameters for theSingleProviderTierOrderingShouldStayUnchanged.
func (fs *FailoverSteps) theSingleProviderTierOrderingShouldStayUnchanged() error {
	if len(fs.rotationOrder) != 1 {
		return fmt.Errorf("expected a single provider, got %v", fs.rotationOrder)
	}
	if !reflect.DeepEqual(fs.rotationOrder, fs.configured) {
		return fmt.Errorf("expected single-provider order to stay unchanged, got %v", fs.rotationOrder)
	}
	return nil
}

// withClearedProviderEnv ...
//
// Expected: parameters for withClearedProviderEnv.
//
// Returns: result of withClearedProviderEnv.
//
// Side effects: None.
func withClearedProviderEnv(fn func() error) error {
	envVars := []string{
		"ANTHROPIC_API_KEY",
		"GITHUB_TOKEN",
		"OLLAMA_CLOUD_API_KEY",
		"OPENAI_API_KEY",
		"OPENCODE_GO_API_KEY",
		"OPENZEN_API_KEY",
		"ZAI_API_KEY",
	}
	originals := make(map[string]string, len(envVars))
	present := make(map[string]bool, len(envVars))

	for _, name := range envVars {
		if value, ok := os.LookupEnv(name); ok {
			originals[name] = value
			present[name] = true
		}
		_ = os.Unsetenv(name)
	}

	defer func() {
		for _, name := range envVars {
			if present[name] {
				_ = os.Setenv(name, originals[name])
				continue
			}
			_ = os.Unsetenv(name)
		}
	}()

	return fn()
}

// loadConfigDrivenConfig ...
//
// Returns: result of loadConfigDrivenConfig.
//
// Side effects: None.
//
// Expected: parameters for loadConfigDrivenConfig.
func (fs *FailoverSteps) loadConfigDrivenConfig() (*config.AppConfig, error) {
	tempDir, err := os.MkdirTemp("", "failover-config-chain-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)

	configPath := filepath.Join(tempDir, "config.yaml")
	content := fs.configDrivenConfigYAML()
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		return nil, err
	}

	cfg, err := config.LoadConfigFromPath(configPath)
	if err != nil {
		return nil, err
	}

	cfg.Providers = config.ProvidersConfig{Default: fs.defaultProvider}
	for _, pref := range fs.configured {
		if !fs.eligible[pref] {
			continue
		}
		switch pref.Provider {
		case "anthropic":
			cfg.Providers.Anthropic = config.ProviderConfig{APIKey: pref.Provider + "-key", Model: pref.Model}
		case "openai":
			cfg.Providers.OpenAI = config.ProviderConfig{APIKey: pref.Provider + "-key", Model: pref.Model}
		case "ollama":
			cfg.Providers.Ollama = config.ProviderConfig{Host: "http://localhost:11435", Model: pref.Model}
		case "zai":
			cfg.Providers.ZAI = config.ProviderConfig{APIKey: pref.Provider + "-key", Model: pref.Model}
		case "copilot":
			cfg.Providers.GitHub = config.ProviderConfig{APIKey: pref.Provider + "-key", Model: pref.Model}
		case "openzen":
			cfg.Providers.OpenZen = config.ProviderConfig{APIKey: pref.Provider + "-key", Model: pref.Model}
		case "opencode-go":
			cfg.Providers.OpenCodeGo = config.ProviderConfig{APIKey: pref.Provider + "-key", Model: pref.Model}
		case "ollamacloud":
			cfg.Providers.OllamaCloud = config.ProviderConfig{APIKey: pref.Provider + "-key", Model: pref.Model}
		}
	}

	return cfg, nil
}

// configDrivenConfigYAML ...
//
// Returns: result of configDrivenConfigYAML.
//
// Side effects: None.
//
// Expected: parameters for configDrivenConfigYAML.
func (fs *FailoverSteps) configDrivenConfigYAML() string {
	var b strings.Builder
	b.WriteString("providers:\n")
	if fs.defaultProvider != "" {
		fmt.Fprintf(&b, "  default: %q\n", fs.defaultProvider)
	}

	for _, pref := range fs.configured {
		if !fs.eligible[pref] {
			continue
		}
		fs.writeProviderConfigYAML(&b, pref.Provider, pref.Model)
	}

	if !fs.anyEligibleConfigured() {
		if pref, ok := fs.configuredModel(fs.defaultProvider); ok {
			fs.writeProviderConfigYAML(&b, pref.Provider, pref.Model)
		}
	}

	return b.String()
}

// anyEligibleConfigured ...
//
// Returns: result of anyEligibleConfigured.
//
// Side effects: None.
//
// Expected: parameters for anyEligibleConfigured.
func (fs *FailoverSteps) anyEligibleConfigured() bool {
	for _, pref := range fs.configured {
		if fs.eligible[pref] {
			return true
		}
	}
	return false
}

// writeProviderConfigYAML ...
//
// Expected: parameters for writeProviderConfigYAML.
//
// Side effects: None.
//
// Returns: result of writeProviderConfigYAML.
func (fs *FailoverSteps) writeProviderConfigYAML(builder *strings.Builder, providerName, model string) {
	apiKey := providerName + "-key"
	if providerName == "ollama" {
		fmt.Fprintf(builder, "  %s:\n", providerName)
		fmt.Fprintf(builder, "    host: %q\n", "http://localhost:11435")
		fmt.Fprintf(builder, "    model: %q\n", model)
		return
	}

	fmt.Fprintf(builder, "  %s:\n", providerName)
	fmt.Fprintf(builder, "    api_key: %q\n", apiKey)
	fmt.Fprintf(builder, "    model: %q\n", model)
}

// seedEquivalentAttempts ...
//
// Side effects: None.
//
// Expected: parameters for seedEquivalentAttempts.
// Returns: result of seedEquivalentAttempts.
func (fs *FailoverSteps) seedEquivalentAttempts() {
	if fs.manager == nil || len(fs.equivalentSpecs) == 0 {
		return
	}
	specs := make([]equivalentProviderSpec, len(fs.equivalentSpecs))
	copy(specs, fs.equivalentSpecs)
	sort.SliceStable(specs, func(i, j int) bool {
		if specs[i].lastUsed == specs[j].lastUsed {
			return i < j
		}
		return specs[i].lastUsed < specs[j].lastUsed
	})
	for _, spec := range specs {
		for range spec.lastUsed {
			fs.manager.RecordAttempt(spec.pref.Provider, spec.pref.Model)
		}
	}
}

// rotationOrderByLeastRecentlyUsed ...
//
// Returns: result of rotationOrderByLeastRecentlyUsed.
//
// Side effects: None.
//
// Expected: parameters for rotationOrderByLeastRecentlyUsed.
func (fs *FailoverSteps) rotationOrderByLeastRecentlyUsed() []provider.ModelPreference {
	if len(fs.equivalentSpecs) == 0 {
		return nil
	}
	grouped := make(map[string][]equivalentProviderSpec)
	groupOrder := make([]string, 0, len(fs.equivalentSpecs))
	for _, spec := range fs.equivalentSpecs {
		if _, ok := grouped[spec.tier]; !ok {
			groupOrder = append(groupOrder, spec.tier)
		}
		grouped[spec.tier] = append(grouped[spec.tier], spec)
	}
	ordered := make([]provider.ModelPreference, 0, len(fs.equivalentSpecs))
	for _, tier := range groupOrder {
		specs := grouped[tier]
		sort.SliceStable(specs, func(i, j int) bool {
			if specs[i].lastUsed == specs[j].lastUsed {
				return i < j
			}
			return specs[i].lastUsed < specs[j].lastUsed
		})
		for _, spec := range specs {
			ordered = append(ordered, spec.pref)
		}
	}
	return ordered
}
