package support

import (
	"os"
	"testing"

	"github.com/cucumber/godog"
	"github.com/cucumber/godog/colors"
)

func getOptions() *godog.Options {
	return &godog.Options{
		Output: colors.Colored(os.Stdout),
		Format: "progress",
		Strict: true,
	}
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
	s.RegisterSteps(ctx)
}
