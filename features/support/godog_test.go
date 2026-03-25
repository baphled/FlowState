// Package support provides BDD test step definitions and helpers.
package support

import (
	"os"
	"testing"

	"github.com/cucumber/godog"
	"github.com/cucumber/godog/colors"
)

func getOptions() *godog.Options {
	opts := &godog.Options{
		Output: colors.Colored(os.Stdout),
		Format: "progress",
		Strict: true,
	}
	if tags := os.Getenv("GODOG_TAGS"); tags != "" {
		opts.Tags = tags
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
	RegisterSessionEnrichmentSteps(ctx)
	s.RegisterSteps(ctx)
}
