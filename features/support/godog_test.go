//go:build e2e

// Package support provides BDD test step definitions and helpers.
package support

import (
	"os"
	"testing"

	"github.com/cucumber/godog"
	"github.com/cucumber/godog/colors"
)

// defaultTagFilter is applied when GODOG_TAGS is unset. It excludes
// scenarios tagged @wip (work-in-progress specifications whose product
// code may exist but whose BDD step glue has not yet been wired). Those
// scenarios are still runnable explicitly via `GODOG_TAGS=@wip` or the
// `make bdd-wip` target, which keeps them findable without breaking the
// default `go test ./features/...` gate under Strict:true.
const defaultTagFilter = "~@wip"

func getOptions() *godog.Options {
	opts := &godog.Options{
		Output: colors.Colored(os.Stdout),
		Format: "progress",
		Strict: true,
	}
	if tags := os.Getenv("GODOG_TAGS"); tags != "" {
		opts.Tags = tags
	} else {
		opts.Tags = defaultTagFilter
	}
	if f := os.Getenv("GODOG_FORMAT"); f != "" {
		opts.Format = f
	}
	// GODOG_PATHS narrows the suite to specific feature files or dirs,
	// enabling scoped runs (e.g. a single feature) without dragging in
	// the whole suite — critical because some shared scenarios use
	// long-backoff retry loops that exceed go test timeouts.
	if p := os.Getenv("GODOG_PATHS"); p != "" {
		opts.Paths = []string{p}
	}
	return opts
}

func TestFeatures(t *testing.T) {
	opts := getOptions()
	if len(opts.Paths) == 0 {
		opts.Paths = []string{"../"}
	}
	opts.TestingT = t

	suite := godog.TestSuite{
		Name:                "flowstate",
		ScenarioInitializer: InitializeScenario,
		Options:             opts,
	}

	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

func InitializeScenario(ctx *godog.ScenarioContext) {
	s := &StepDefinitions{}
	RegisterOAuthSteps(ctx, s)
	RegisterSkillSteps(ctx, s)
	RegisterMemorySteps(ctx)
	RegisterSkillAutoloadingSteps(ctx)
	RegisterHarnessSteps(ctx)
	RegisterSessionEnrichmentSteps(ctx, s)
	RegisterSessionForkSteps(ctx, s)
	RegisterMultilineInputSteps(ctx, s)
	si := &SessionIsolationSteps{}
	RegisterSessionIsolationSteps(ctx, si)
	RegisterSessionVisibilitySteps(ctx, si)
	sto := &StreamingToolOutputSteps{}
	RegisterStreamingToolOutputSteps(ctx, sto)
	// Planning steps are in the same package
	RegisterPlanningSteps(ctx)
	RegisterOrchestratorMetadataSteps(ctx)
	RegisterConfigSteps(ctx)
	RegisterPluginSteps(ctx)
	RegisterFailoverSteps(ctx)
	RegisterDelegationSessionSteps(ctx)
	s.RegisterSteps(ctx)
	s.RegisterAgentLayeringSteps(ctx)
	RegisterStreamingCancelSteps(ctx, s)
	initPlanHarnessE2ESteps(ctx)
	initPlanRejectionLoopSteps(ctx)
	RegisterExecutionLoopSteps(ctx)
	RegisterLearningLoopSteps(ctx)
	RegisterCompressionSteps(ctx)
	RegisterAutoCompactionSteps(ctx)
	RegisterSessionMemorySteps(ctx)
	RegisterCompressionE2ESteps(ctx)
	RegisterRecallLearningSteps(ctx)
	RegisterLearningBridgeSteps(ctx)
	RegisterAdultingMemorySteps(ctx)
	RegisterFSPollutionSteps(ctx)
	RegisterPersistenceCompletenessSteps(ctx)
	RegisterTargetSpecificitySteps(ctx)
	RegisterAdultingDeadlineSteps(ctx)
	RegisterVaultIndexSyncSteps(ctx)
	RegisterVaultQueryCollectionSteps(ctx)
	RegisterTodoSteps(ctx)
	RegisterMCPServerLifecycleSteps(ctx)
	RegisterGateAmendmentSteps(ctx)
	RegisterNotificationStreamSteps(ctx)
	RegisterDelegationIntegritySteps(ctx)
	VoiceTalkContext(ctx)
	VoiceCLIContext(ctx)
	VoiceTTSContext(ctx)
}
