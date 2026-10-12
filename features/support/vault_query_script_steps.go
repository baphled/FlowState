//go:build e2e

package support

import (
	"errors"
	"regexp"

	"io/fs"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/app"
)

var (
	errNoEmbeddedQueryScript   = errors.New("embedded query-vault script could not be read")
	errScriptSetsLLM           = errors.New("query-vault script still sets Settings.llm, which triggers LlamaIndex's MockLLM notice")
	errScriptBuildsQueryEngine = errors.New("query-vault script still builds a query engine; it must call retriever.retrieve directly")
	errScriptLacksRetrieve     = errors.New("query-vault script does not call retrieve( directly")
	errScriptLacksSources      = errors.New("query-vault script no longer prints a Sources section")
)

// RegisterVaultQueryScriptSteps registers the retrieval-only output
// scenario steps with the Godog scenario context.
func RegisterVaultQueryScriptSteps(ctx *godog.ScenarioContext) {
	var script string

	ctx.Step(`^I inspect the embedded query-vault script$`, func() error {
		data, err := fs.ReadFile(app.EmbeddedVaultToolsFS(), app.VaultToolsEmbedSubdir+"/query-vault")
		if err != nil {
			return errNoEmbeddedQueryScript
		}
		script = string(data)
		return nil
	})
	ctx.Step(`^the script should not configure an explicit LLM$`, func() error {
		if regexp.MustCompile(`Settings\.llm`).FindString(script) != "" {
			return errScriptSetsLLM
		}
		return nil
	})
	ctx.Step(`^the script should not build a query engine$`, func() error {
		if regexp.MustCompile(`QueryEngine|query_engine`).FindString(script) != "" {
			return errScriptBuildsQueryEngine
		}
		return nil
	})
	ctx.Step(`^the script should retrieve nodes directly from the retriever$`, func() error {
		if regexp.MustCompile(`retriever\.retrieve\(`).FindString(script) == "" {
			return errScriptLacksRetrieve
		}
		return nil
	})
	ctx.Step(`^the script should still print a Sources section$`, func() error {
		if regexp.MustCompile(`Sources:`).FindString(script) == "" {
			return errScriptLacksSources
		}
		return nil
	})
}
