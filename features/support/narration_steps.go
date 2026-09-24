package support

import (
	"context"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/voice"
	"github.com/cucumber/godog"
)

// narrationState holds the narration under test for one scenario.
type narrationState struct {
	got string
}

// narration is the per-scenario narration state.
var narration *narrationState

// NarrationContext registers the tool-call narration steps.
//
// Expected:
//   - sc is a Godog scenario context for the features under test.
//
// Returns:
//   - None.
//
// Side effects:
//   - Registers step definitions and resets the shared narration
//     state before and after each scenario.
func NarrationContext(sc *godog.ScenarioContext) {
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		narration = &narrationState{}
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		narration = nil
		return ctx, nil
	})

	sc.Step(`^I narrate a "([^"]*)" tool call with input (.*)$`, iNarrateToolCall)
	sc.Step(`^I narrate turn text (.*)$`, iNarrateTurnText)
	sc.Step(`^I narrate a (tool_result|tool_error) event with content (.*)$`, iNarrateToolEvent)
	sc.Step(`^the narration does not contain (.*)$`, theNarrationDoesNotContain)
	sc.Step(`^the narration mentions "(.*)"$`, theNarrationMentions)
	sc.Step(`^the narration is at most (\d+) characters$`, theNarrationIsAtMost)
}

// iNarrateToolCall runs NarrateToolCall on a tool call event.
//
// Expected:
//   - name is a tool name and input is its raw input.
//
// Returns:
//   - Always nil.
//
// Side effects:
//   - Stores the narrated result in the shared narration state.
func iNarrateToolCall(name, input string) error {
	narration.got = voice.NarrateToolCall(name, input)
	return nil
}

// iNarrateTurnText runs PreprocessTTSTurn on a text fragment.
//
// Expected:
//   - text is a text delta from a turn event stream.
//
// Returns:
//   - Always nil.
//
// Side effects:
//   - Stores the preprocessed fragment in the shared narration state.
func iNarrateTurnText(text string) error {
	narration.got = voice.PreprocessTTSTurn(text)
	return nil
}

// iNarrateToolEvent runs NarrateToolOutcome on an outcome event.
//
// Expected:
//   - kind is "tool_result" or "tool_error"; content is unused.
//
// Returns:
//   - Always nil.
//
// Side effects:
//   - Stores the narrated outcome in the shared narration state.
func iNarrateToolEvent(kind, content string) error {
	_ = content
	narration.got = voice.NarrateToolOutcome(kind)
	return nil
}

// theNarrationDoesNotContain asserts an absent substring after
// unescaping common C-style escapes.
//
// Expected:
//   - The narrated output does not include the unescaped needle.
//
// Returns:
//   - An error describing the failure when the needle is present;
//     nil otherwise.
//
// Side effects:
//   - None.
func theNarrationDoesNotContain(needle string) error {
	if strings.Contains(narration.got, unescapeNarration(needle)) {
		return fmt.Errorf("narration %q contains %q", narration.got, needle)
	}
	return nil
}

// theNarrationMentions asserts a present substring.
//
// Expected:
//   - The narrated output includes want, case-insensitively.
//
// Returns:
//   - An error describing the failure when want is absent; nil
//     otherwise.
//
// Side effects:
//   - None.
func theNarrationMentions(want string) error {
	if !strings.Contains(strings.ToLower(narration.got), strings.ToLower(want)) {
		return fmt.Errorf("narration %q does not mention %q", narration.got, want)
	}
	return nil
}

// theNarrationIsAtMost asserts a length ceiling on the narration.
//
// Expected:
//   - The narrated output is no longer than max characters.
//
// Returns:
//   - An error describing the failure when the ceiling is exceeded;
//     nil otherwise.
//
// Side effects:
//   - None.
func theNarrationIsAtMost(max int) error {
	if len(narration.got) > max {
		return fmt.Errorf("narration len %d exceeds %d", len(narration.got), max)
	}
	return nil
}

// unescapeNarration converts the literal \n sequences written in
// feature files into real newlines for substring checks.
//
// Expected:
//   - s contains literal "\n" sequences as written in Gherkin steps.
//
// Returns:
//   - s with every literal "\n" replaced by a real newline character.
//
// Side effects:
//   - None.
func unescapeNarration(s string) string {
	return strings.ReplaceAll(s, "\\n", "\n")
}
