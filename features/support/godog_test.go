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
	return opts
}

func TestFeatures(t *testing.T) {
	opts := getOptions()
	opts.Paths = []string{"../"}
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
	RegisterSessionDeleteSteps(ctx)
	RegisterMultilineInputSteps(ctx, s)
	si := &SessionIsolationSteps{}
	RegisterSessionIsolationSteps(ctx, si)
	RegisterSessionVisibilitySteps(ctx, si)
	sto := &StreamingToolOutputSteps{}
	RegisterStreamingToolOutputSteps(ctx, sto)
	rts := &ReadToolSteps{}
	RegisterReadToolSteps(ctx, rts)
	// Planning steps are in the same package
	RegisterPlanningSteps(ctx)
	RegisterOrchestratorMetadataSteps(ctx)
	RegisterConfigSteps(ctx)
	ss := &ScrollingSteps{}
	RegisterScrollingSteps(ctx, ss)
	RegisterPluginSteps(ctx)
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
	RegisterDualPaneLayoutSteps(ctx)
	RegisterSessionTreeNavigationSteps(ctx)
	RegisterSwarmActivityTimelineSteps(ctx)
	RegisterMultiAgentChatUXE2ESteps(ctx)
	RegisterRecallLearningSteps(ctx)
}
