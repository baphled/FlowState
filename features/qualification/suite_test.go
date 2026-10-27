package qualification

import (
	"os"
	"testing"

	"github.com/cucumber/godog"
)

func TestQualification(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			TestingT: t,
			Paths:    []string{"../../features/qualification"},
			Format:   "pretty",
			Output:   os.Stdout,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("qualification suite failed")
	}
}
