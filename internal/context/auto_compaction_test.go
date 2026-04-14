// Package context_test — Layer 2 AutoCompactor specification.
//
// These tests pin the T9b contract: AutoCompactor renders the T8 prompt,
// calls an injected Summariser, parses the JSON response into a
// CompactionSummary, and validates that Intent and NextSteps are non-empty.
// It never retries. It never imports provider or engine directly — the
// Summariser is a narrow local interface so the consumer package remains
// cycle-free.
package context_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

// fakeSummariser is a test double implementing contextpkg.Summariser. It
// records the inputs it receives and returns a scripted response (or error)
// to the caller. Deliberately bare-bones — no sync primitives — because
// AutoCompactor is exercised serially from each test.
type fakeSummariser struct {
	// recordedSystem and recordedUser capture the last call's prompts so
	// assertions can verify that AutoCompactor is threading the T8
	// SummaryPromptSystem and rendered user prompt through verbatim.
	recordedSystem string
	recordedUser   string
	// recordedMessages captures the msgs slice AutoCompactor was asked to
	// summarise, so tests can assert the slice reached the summariser
	// unchanged.
	recordedMessages []provider.Message

	// response is the text returned from Summarise when err is nil.
	response string
	// err is returned directly by Summarise when non-nil.
	err error

	// calls counts invocations; used to assert "no retries" by checking
	// that exactly one call happened even after the single attempt errors.
	calls int
}

// Summarise implements contextpkg.Summariser. It records inputs, increments
// the call counter, and returns the scripted result.
func (f *fakeSummariser) Summarise(_ context.Context, systemPrompt string, userPrompt string, msgs []provider.Message) (string, error) {
	f.calls++
	f.recordedSystem = systemPrompt
	f.recordedUser = userPrompt
	f.recordedMessages = msgs
	if f.err != nil {
		return "", f.err
	}
	return f.response, nil
}

// sampleSummaryJSON builds a JSON body the fake summariser can return. It
// matches the T7 CompactionSummary schema exactly — field names in snake
// case. Keeping it constructed rather than inlined as a raw string makes
// it easier to vary per-test without hand-escaping quotes.
func sampleSummaryJSON(t *testing.T, override func(*contextpkg.CompactionSummary)) string {
	t.Helper()

	summary := contextpkg.CompactionSummary{
		Intent:             "summarise the current compaction slice",
		KeyDecisions:       []string{"route via summariser", "never retry"},
		Errors:             []string{},
		NextSteps:          []string{"persist result"},
		FilesToRestore:     []string{"internal/context/auto_compaction.go"},
		OriginalTokenCount: 4200,
		SummaryTokenCount:  640,
	}
	if override != nil {
		override(&summary)
	}
	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("build sample summary JSON: %v", err)
	}
	return string(data)
}

// sampleMessages is a small, stable slice of provider.Messages used as
// input to AutoCompactor.Compact across tests. Content is deliberately
// terse — we are exercising orchestration, not prompt fidelity.
func sampleMessages() []provider.Message {
	return []provider.Message{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "follow-up"},
	}
}

// TestAutoCompactor_Compact_HappyPath_ProducesSummary is the central happy
// path: given a summariser that returns valid JSON, AutoCompactor.Compact
// returns a populated CompactionSummary with the intent and next_steps
// fields surviving the round-trip.
func TestAutoCompactor_Compact_HappyPath_ProducesSummary(t *testing.T) {
	t.Parallel()

	summariser := &fakeSummariser{
		response: sampleSummaryJSON(t, nil),
	}
	compactor := contextpkg.NewAutoCompactor(summariser)

	summary, err := compactor.Compact(context.Background(), sampleMessages())
	if err != nil {
		t.Fatalf("Compact: unexpected error: %v", err)
	}
	if summary.Intent == "" {
		t.Fatalf("Compact: Intent is empty; want non-empty")
	}
	if len(summary.NextSteps) == 0 {
		t.Fatalf("Compact: NextSteps is empty; want at least one entry")
	}
	if summariser.calls != 1 {
		t.Fatalf("Compact: summariser calls = %d; want exactly 1", summariser.calls)
	}
}

// TestAutoCompactor_Compact_ThreadsT8Prompts_Verbatim asserts that
// AutoCompactor passes SummaryPromptSystem and the RenderSummaryPrompt
// output to the summariser untouched. This is the integration point with
// T8 — a regression here would mean the forbidden-ids directive or the
// schema contract never reaches the model.
func TestAutoCompactor_Compact_ThreadsT8Prompts_Verbatim(t *testing.T) {
	t.Parallel()

	summariser := &fakeSummariser{
		response: sampleSummaryJSON(t, nil),
	}
	compactor := contextpkg.NewAutoCompactor(summariser)

	msgs := sampleMessages()
	if _, err := compactor.Compact(context.Background(), msgs); err != nil {
		t.Fatalf("Compact: unexpected error: %v", err)
	}

	if summariser.recordedSystem != contextpkg.SummaryPromptSystem {
		t.Fatalf("recordedSystem does not match SummaryPromptSystem exactly; system drift detected")
	}

	wantUser, err := contextpkg.RenderSummaryPrompt(msgs)
	if err != nil {
		t.Fatalf("RenderSummaryPrompt: %v", err)
	}
	if summariser.recordedUser != wantUser {
		t.Fatalf("recordedUser does not match RenderSummaryPrompt output; user prompt drift")
	}

	if len(summariser.recordedMessages) != len(msgs) {
		t.Fatalf("recordedMessages length = %d; want %d", len(summariser.recordedMessages), len(msgs))
	}
}

// TestAutoCompactor_Compact_EmptyMessages_ReturnsErrEmpty asserts that
// Compact refuses an empty input rather than calling the summariser with
// a prompt that has nothing to summarise. The error should surface the
// upstream ErrEmptySummaryInput sentinel so the caller can distinguish it
// from a summariser-level failure.
func TestAutoCompactor_Compact_EmptyMessages_ReturnsErrEmpty(t *testing.T) {
	t.Parallel()

	summariser := &fakeSummariser{}
	compactor := contextpkg.NewAutoCompactor(summariser)

	_, err := compactor.Compact(context.Background(), nil)
	if err == nil {
		t.Fatalf("Compact: expected error for empty messages; got nil")
	}
	if !errors.Is(err, contextpkg.ErrEmptySummaryInput) {
		t.Fatalf("Compact: err = %v; want ErrEmptySummaryInput", err)
	}
	if summariser.calls != 0 {
		t.Fatalf("Compact: summariser should not be called for empty input; got %d calls", summariser.calls)
	}
}

// TestAutoCompactor_Compact_SummariserError_PropagatesAndDoesNotRetry
// asserts that a summariser-level error is returned wrapped (so the caller
// sees context) and that Compact performs exactly one attempt. The "no
// retries" rule comes directly from the T9b spec.
func TestAutoCompactor_Compact_SummariserError_PropagatesAndDoesNotRetry(t *testing.T) {
	t.Parallel()

	upstream := errors.New("summariser: simulated provider outage")
	summariser := &fakeSummariser{err: upstream}
	compactor := contextpkg.NewAutoCompactor(summariser)

	_, err := compactor.Compact(context.Background(), sampleMessages())
	if err == nil {
		t.Fatalf("Compact: expected error propagated from summariser; got nil")
	}
	if !errors.Is(err, upstream) {
		t.Fatalf("Compact: err = %v; want wrapped %v", err, upstream)
	}
	if summariser.calls != 1 {
		t.Fatalf("Compact: summariser calls = %d; want exactly 1 (no retries)", summariser.calls)
	}
}

// TestAutoCompactor_Compact_MalformedJSON_ReturnsParseError asserts that
// when the summariser returns a string that is not valid JSON, Compact
// returns a descriptive error rather than a zero-value summary. The error
// must not be ErrEmptySummaryInput — that sentinel is reserved for empty
// input only.
func TestAutoCompactor_Compact_MalformedJSON_ReturnsParseError(t *testing.T) {
	t.Parallel()

	summariser := &fakeSummariser{response: "not { valid json"}
	compactor := contextpkg.NewAutoCompactor(summariser)

	_, err := compactor.Compact(context.Background(), sampleMessages())
	if err == nil {
		t.Fatalf("Compact: expected parse error; got nil")
	}
	if errors.Is(err, contextpkg.ErrEmptySummaryInput) {
		t.Fatalf("Compact: err = %v; should not be ErrEmptySummaryInput", err)
	}
	if !strings.Contains(err.Error(), "parse") && !strings.Contains(err.Error(), "unmarshal") {
		t.Fatalf("Compact: err = %q; want message mentioning parse/unmarshal", err.Error())
	}
}

// TestAutoCompactor_Compact_MissingIntent_ReturnsValidationError asserts
// that a summary whose Intent is empty — even if the JSON parses — is
// rejected. Intent is the semantic anchor for rehydration; without it the
// summary is useless and downstream persistence must not occur.
func TestAutoCompactor_Compact_MissingIntent_ReturnsValidationError(t *testing.T) {
	t.Parallel()

	summariser := &fakeSummariser{
		response: sampleSummaryJSON(t, func(s *contextpkg.CompactionSummary) {
			s.Intent = ""
		}),
	}
	compactor := contextpkg.NewAutoCompactor(summariser)

	_, err := compactor.Compact(context.Background(), sampleMessages())
	if err == nil {
		t.Fatalf("Compact: expected validation error for empty Intent; got nil")
	}
	if !errors.Is(err, contextpkg.ErrInvalidSummary) {
		t.Fatalf("Compact: err = %v; want ErrInvalidSummary", err)
	}
	if !strings.Contains(err.Error(), "intent") {
		t.Fatalf("Compact: err = %q; want message naming the missing field", err.Error())
	}
}

// TestAutoCompactor_Compact_MissingNextSteps_ReturnsValidationError asserts
// that a summary whose NextSteps slice is empty is rejected. NextSteps is
// the continuation anchor; without it the agent has nowhere to resume.
func TestAutoCompactor_Compact_MissingNextSteps_ReturnsValidationError(t *testing.T) {
	t.Parallel()

	summariser := &fakeSummariser{
		response: sampleSummaryJSON(t, func(s *contextpkg.CompactionSummary) {
			s.NextSteps = nil
		}),
	}
	compactor := contextpkg.NewAutoCompactor(summariser)

	_, err := compactor.Compact(context.Background(), sampleMessages())
	if err == nil {
		t.Fatalf("Compact: expected validation error for empty NextSteps; got nil")
	}
	if !errors.Is(err, contextpkg.ErrInvalidSummary) {
		t.Fatalf("Compact: err = %v; want ErrInvalidSummary", err)
	}
	if !strings.Contains(err.Error(), "next_steps") {
		t.Fatalf("Compact: err = %q; want message naming the missing field", err.Error())
	}
}

// TestAutoCompactor_Compact_NilSummariser_ReturnsConfigError asserts that
// constructing an AutoCompactor with a nil summariser surfaces as a typed
// error on the first call rather than a nil-pointer panic.
func TestAutoCompactor_Compact_NilSummariser_ReturnsConfigError(t *testing.T) {
	t.Parallel()

	compactor := contextpkg.NewAutoCompactor(nil)

	_, err := compactor.Compact(context.Background(), sampleMessages())
	if err == nil {
		t.Fatalf("Compact: expected error for nil summariser; got nil")
	}
	if !errors.Is(err, contextpkg.ErrNilSummariser) {
		t.Fatalf("Compact: err = %v; want ErrNilSummariser", err)
	}
}

// TestAutoCompactor_Compact_FenceWithoutNewline_ReturnsParseError
// exercises the defensive branch in stripJSONFences where the summariser
// returns a response that starts with ``` but contains no subsequent
// newline. The fence-stripper passes the raw input through unchanged
// (there is nothing sensible to strip), so json.Unmarshal rejects it as
// malformed. Added for coverage on the stripJSONFences.newlineIdx == -1
// branch — keeping the contract explicit as a byproduct.
func TestAutoCompactor_Compact_FenceWithoutNewline_ReturnsParseError(t *testing.T) {
	t.Parallel()

	summariser := &fakeSummariser{response: "```json no-newline-ever"}
	compactor := contextpkg.NewAutoCompactor(summariser)

	_, err := compactor.Compact(context.Background(), sampleMessages())
	if err == nil {
		t.Fatalf("Compact: expected parse error for fence-without-newline; got nil")
	}
	if errors.Is(err, contextpkg.ErrInvalidSummary) {
		t.Fatalf("Compact: err = %v; should not be ErrInvalidSummary", err)
	}
}

// TestAutoCompactor_Compact_FencedJSON_ParsesSuccessfully asserts that a
// summariser returning a JSON body wrapped in Markdown code fences (a
// common misbehaviour by chat models despite the T8 prompt's instruction)
// is still parsed. The T8 system prompt forbids fences but defensive
// parsing keeps us robust against minor model drift without enabling
// free-form prose.
func TestAutoCompactor_Compact_FencedJSON_ParsesSuccessfully(t *testing.T) {
	t.Parallel()

	raw := sampleSummaryJSON(t, nil)
	fenced := "```json\n" + raw + "\n```"
	summariser := &fakeSummariser{response: fenced}
	compactor := contextpkg.NewAutoCompactor(summariser)

	summary, err := compactor.Compact(context.Background(), sampleMessages())
	if err != nil {
		t.Fatalf("Compact: unexpected error for fenced JSON: %v", err)
	}
	if summary.Intent == "" {
		t.Fatalf("Compact: fenced JSON was parsed but Intent is empty; want populated")
	}
}
