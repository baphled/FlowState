// Package context — Layer 2 AutoCompactor (T9b).
//
// AutoCompactor is the orchestration layer that turns a slice of cold
// messages into a CompactionSummary. It renders the T8 prompts, hands
// them to an injected Summariser, parses the JSON response, and validates
// the minimum semantic contract (Intent and NextSteps non-empty).
//
// Design choices:
//
//   - The Summariser is a narrow local interface, not provider.Provider
//     and not engine.SummariserResolver. This keeps internal/context free
//     of provider and engine imports, avoiding the cycle that the plan
//     flagged (engine imports context; context cannot import engine).
//     The wiring between a CategoryConfig and an actual Chat call is done
//     in internal/engine where both sides are already visible.
//
//   - No retries. A single failed summarisation surfaces as an error; the
//     caller (engine integration in T10) decides whether to fall back or
//     give up. This matches the plan's explicit "no retries" directive.
//
//   - Validation is semantic, not structural. JSON unmarshalling handles
//     structure; AutoCompactor only enforces that Intent and NextSteps
//     are populated — the two fields without which rehydration (T11) has
//     nothing to anchor on.
package context

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/baphled/flowstate/internal/provider"
)

// ErrInvalidSummary is returned when the summariser produces a parseable
// but semantically empty summary. Exposed as a sentinel so callers (and
// tests) can distinguish validation failure from transport or parse
// failure.
var ErrInvalidSummary = errors.New("context: compaction summary failed validation")

// ErrNilSummariser is returned when AutoCompactor.Compact is invoked on
// an instance constructed with a nil Summariser. We return an error
// rather than panic so misconfiguration surfaces through normal error
// handling at the integration point.
var ErrNilSummariser = errors.New("context: auto-compactor summariser is nil")

// ErrRehydrationRead is returned when Rehydrate fails to read a file
// listed in CompactionSummary.FilesToRestore. The wrapped error carries
// the underlying filesystem error; the message contains the offending
// path so operators can act on it without parsing the chain. Exposed as
// a sentinel so callers can distinguish rehydration I/O failures from
// summary validation failures (ErrInvalidSummary).
var ErrRehydrationRead = errors.New("context: rehydration read failed")

// Summariser is the narrow capability AutoCompactor depends on to obtain
// a textual summary from an LLM. Implementations live outside this
// package (internal/engine binds a CategoryConfig-routed provider.Provider
// to this interface); this file intentionally contains no concrete
// implementation beyond the orchestrator.
//
// Expected:
//   - ctx carries cancellation/deadline for the remote call.
//   - systemPrompt is the fixed T8 SummaryPromptSystem.
//   - userPrompt is the rendered T8 user prompt produced by
//     RenderSummaryPrompt.
//   - msgs is the original slice of messages being summarised; supplied
//     so implementations that require structured input (e.g. tool-call
//     echoing, caching keys) have access without re-parsing userPrompt.
//
// Returns:
//   - The raw text of the model's response on success. No processing
//     (trimming, fence stripping) is required of the implementation;
//     AutoCompactor handles cleanup.
//   - Any error returned is propagated verbatim (wrapped) to the caller
//     of AutoCompactor.Compact.
type Summariser interface {
	// Summarise invokes the remote summariser with the T8 system and
	// user prompts, returning the model's raw textual response. See the
	// Summariser interface doc for the expected contract.
	Summarise(ctx context.Context, systemPrompt string, userPrompt string, msgs []provider.Message) (string, error)
}

// AutoCompactor produces CompactionSummary values from cold message
// ranges. It is a stateless orchestrator — all state lives on the
// injected Summariser or in the returned summary.
type AutoCompactor struct {
	summariser Summariser
}

// NewAutoCompactor builds an AutoCompactor bound to the given Summariser.
//
// Expected:
//   - summariser may be nil; in that case every Compact call returns
//     ErrNilSummariser so misconfiguration surfaces as a typed error at
//     the first use rather than at construction.
//
// Returns:
//   - An AutoCompactor. Never nil.
//
// Side effects:
//   - None.
func NewAutoCompactor(summariser Summariser) *AutoCompactor {
	return &AutoCompactor{summariser: summariser}
}

// Compact turns a slice of messages into a CompactionSummary via the
// injected Summariser. It threads the T8 SummaryPromptSystem and the
// rendered user prompt through unchanged, so prompt-level contracts
// (forbidden-ids, JSON-only) remain enforceable by the summariser.
//
// Expected:
//   - ctx is a valid context; cancellation is honoured by the underlying
//     Summariser implementation.
//   - msgs is the slice of cold messages to summarise. An empty slice is
//     rejected with ErrEmptySummaryInput rather than sent to the model.
//
// Returns:
//   - A populated CompactionSummary on success. OriginalTokenCount and
//     SummaryTokenCount are left as emitted by the summariser — the
//     caller (T10 persistence) overwrites them with authoritative values
//     derived from the token counter.
//   - ErrEmptySummaryInput when msgs is empty.
//   - ErrNilSummariser when the compactor was constructed without one.
//   - A wrapped parse error when the summariser response is not valid
//     JSON (after fence stripping).
//   - ErrInvalidSummary when the parsed summary is missing Intent or
//     NextSteps.
//   - Any summariser error wrapped with context for diagnostics.
//
// Side effects:
//   - One call to Summariser.Summarise. No retries; no persistence.
func (a *AutoCompactor) Compact(ctx context.Context, msgs []provider.Message) (CompactionSummary, error) {
	if a.summariser == nil {
		return CompactionSummary{}, ErrNilSummariser
	}
	if len(msgs) == 0 {
		return CompactionSummary{}, ErrEmptySummaryInput
	}

	userPrompt, err := RenderSummaryPrompt(msgs)
	if err != nil {
		return CompactionSummary{}, fmt.Errorf("auto-compactor: render prompt: %w", err)
	}

	raw, err := a.summariser.Summarise(ctx, SummaryPromptSystem, userPrompt, msgs)
	if err != nil {
		return CompactionSummary{}, fmt.Errorf("auto-compactor: summariser failed: %w", err)
	}

	cleaned := stripJSONFences(raw)

	var summary CompactionSummary
	if err := json.Unmarshal([]byte(cleaned), &summary); err != nil {
		return CompactionSummary{}, fmt.Errorf("auto-compactor: parse summary JSON: %w", err)
	}

	if err := validateSummary(summary); err != nil {
		return CompactionSummary{}, err
	}

	return summary, nil
}

// stripJSONFences removes a surrounding Markdown code fence if the model
// produced one. The T8 prompt forbids fences, but defensive parsing keeps
// us robust against minor model drift. Only outer fences are stripped;
// embedded fences inside string fields (unlikely) are preserved.
//
// Expected:
//   - raw is the unprocessed text returned by the summariser.
//
// Returns:
//   - The input with a leading ```json / ``` fence and trailing ``` fence
//     removed if both are present; otherwise the input unchanged (aside
//     from surrounding whitespace, which is always trimmed).
//
// Side effects:
//   - None.
func stripJSONFences(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}

	// Drop the opening fence line (```json or just ```).
	newlineIdx := strings.IndexByte(trimmed, '\n')
	if newlineIdx == -1 {
		return trimmed
	}
	body := trimmed[newlineIdx+1:]
	body = strings.TrimSpace(body)

	// Drop the trailing fence if present.
	if strings.HasSuffix(body, "```") {
		body = strings.TrimSuffix(body, "```")
		body = strings.TrimSpace(body)
	}

	return body
}

// Rehydrate turns a previously produced CompactionSummary back into a
// seed slice of messages that the engine can prepend to the next
// context window. The first message is a system message anchored on the
// summary's Intent ("Session rehydrated. Continuing from: <intent>");
// subsequent messages are one tool-role message per file in
// FilesToRestore, carrying the file's verbatim content.
//
// Expected:
//   - summary.Intent is non-empty. Callers should have produced the
//     summary via Compact, which enforces the same invariant — this is
//     a defensive second check.
//   - Paths in summary.FilesToRestore are readable by the process.
//     Relative paths resolve against the process working directory, so
//     callers that accept relative paths from the model must convert
//     them to absolute before storing the summary.
//
// Returns:
//   - A []provider.Message of length 1 + len(FilesToRestore) on success.
//   - ErrInvalidSummary when Intent is empty.
//   - A wrapped ErrRehydrationRead when any file in FilesToRestore
//     cannot be read. The error message names the offending path. On
//     the first failure Rehydrate stops and does not return partial
//     results — rehydration is all-or-nothing so the engine never runs
//     against a half-populated seed window.
//
// Side effects:
//   - Reads each file in FilesToRestore once via os.ReadFile. No
//     caching, no retries.
func (a *AutoCompactor) Rehydrate(summary CompactionSummary) ([]provider.Message, error) {
	if strings.TrimSpace(summary.Intent) == "" {
		return nil, fmt.Errorf("%w: field intent is empty", ErrInvalidSummary)
	}

	msgs := make([]provider.Message, 0, 1+len(summary.FilesToRestore))
	msgs = append(msgs, provider.Message{
		Role:    "system",
		Content: "Session rehydrated. Continuing from: " + summary.Intent,
	})

	for _, path := range summary.FilesToRestore {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrRehydrationRead, path, err)
		}
		msgs = append(msgs, provider.Message{
			Role:    "tool",
			Content: string(content),
		})
	}

	return msgs, nil
}

// validateSummary enforces the minimum semantic contract: Intent must be
// non-empty (after trimming whitespace) and NextSteps must contain at
// least one entry. Any failure returns a wrapped ErrInvalidSummary with
// the field name embedded for diagnostics.
//
// Expected:
//   - summary is the parsed result of json.Unmarshal.
//
// Returns:
//   - nil when both invariants hold.
//   - A wrapped ErrInvalidSummary naming the first failing field.
//
// Side effects:
//   - None.
func validateSummary(summary CompactionSummary) error {
	if strings.TrimSpace(summary.Intent) == "" {
		return fmt.Errorf("%w: field intent is empty", ErrInvalidSummary)
	}
	if len(summary.NextSteps) == 0 {
		return fmt.Errorf("%w: field next_steps is empty", ErrInvalidSummary)
	}
	return nil
}
