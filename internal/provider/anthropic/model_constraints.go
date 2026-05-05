// Package anthropic — per-model payload contracts.
//
// Different Anthropic Claude models accept different request shapes.
// This file collects the per-model decision tree that adjusts a caller-
// supplied set of parameters before they are marshalled into the
// MessageNewParams sent to the API. The goal is to keep buildRequestParams
// thin and to centralise every "model X rejects field Y" rule so the
// matrix is reviewable in one place.
//
// The rules implemented here come from the Phase 1 research pass:
//
//   - Opus 4.7 (`claude-opus-4-7*`):
//       * rejects non-default `temperature`/`top_p`/`top_k`
//       * rejects manual `thinking: enabled` (must use `adaptive`)
//       * `max_tokens` ceiling is 128k; default `thinking.display`
//         is `omitted`
//   - Opus 4.6 / Sonnet 4.6 (`claude-opus-4-6*`, `claude-sonnet-4-6*`):
//       * manual `thinking: enabled` is deprecated but accepted
//       * `max_tokens` ceiling: Opus 4.6 = 128k, Sonnet 4.6 = 64k
//   - Other Claude 4.x (Opus 4/4.1/4.5, Sonnet 4/4.5, Haiku 4.5):
//       * manual thinking allowed, sampling allowed
//       * `max_tokens` ceiling per matrix
//   - Sonnet 3.7 (`claude-3-7-sonnet*`):
//       * full thinking; supports `output-128k-2025-02-19` and
//         `token-efficient-tools-2025-02-19` betas
//   - Claude 3.5/3.0: no extended thinking
//
// Out of scope here (deferred to Phase 3): cache_control breakpoint
// placement, signature_delta capture, retry-after capture, image/PDF
// blocks, files/batches.
package anthropic

import (
	"errors"
	"fmt"
	"strings"

	anthropicAPI "github.com/anthropics/anthropic-sdk-go"
	"github.com/baphled/flowstate/internal/provider"
)

// errThinking* are returned when caller-supplied thinking parameters
// fail validation against the target model.
var (
	errThinkingBudgetTooLow      = errors.New("anthropic: thinking budget_tokens must be >= 1024")
	errThinkingBudgetExceedsMax  = errors.New("anthropic: thinking budget_tokens must be < max_tokens")
	errThinkingMaxTokensZero     = errors.New("anthropic: thinking requires max_tokens > 0")
	errThinkingToolChoiceInvalid = errors.New("anthropic: thinking only supports tool_choice in {auto, none}")
	errThinkingEnabledRejected   = errors.New("anthropic: model rejects manual thinking: enabled (use adaptive)")
)

// minThinkingBudgetTokens is the API-enforced minimum for
// thinking.budget_tokens when the field is set.
const minThinkingBudgetTokens int64 = 1024

// modelDefaults captures the per-model knobs that vary across the
// Claude family. A caller's request is overlaid on top of these
// defaults; only fields the caller did not set are filled in from here.
type modelDefaults struct {
	// maxTokens is the default generation cap when the caller leaves
	// MaxTokens unset. Zero means "use the package-wide default of
	// 4096" — older models keep the historical 4096 cap.
	maxTokens int64
	// rejectsCustomSampling reports that any non-default
	// temperature/top_p/top_k must be stripped before send (Opus 4.7).
	rejectsCustomSampling bool
	// rejectsManualThinkingEnabled reports that `thinking: enabled`
	// must be rewritten to `adaptive` (Opus 4.7).
	rejectsManualThinkingEnabled bool
	// supportsThinking reports whether the model supports any kind of
	// extended thinking. Older models (3.5/3.0) do not.
	supportsThinking bool
	// supportsAdaptiveThinking reports that the model recognises the
	// `adaptive` thinking variant (Opus 4.7+ family).
	supportsAdaptiveThinking bool
	// betas are anthropic-beta header values to add by default when
	// the caller is on this model. Used for Sonnet 3.7's optional
	// 128k-output and token-efficient-tools betas.
	betas []string
}

// resolveModelDefaults returns the modelDefaults for the given model
// id. Unknown ids fall through to a conservative permissive default
// that matches historical behaviour (4096 max tokens, no thinking).
//
// Expected:
//   - model is the Anthropic model id (e.g. "claude-opus-4-7-20251201").
//
// Returns:
//   - The modelDefaults that apply.
//
// Side effects:
//   - None.
func resolveModelDefaults(model string) modelDefaults {
	id := strings.ToLower(model)

	switch {
	case strings.HasPrefix(id, "claude-opus-4-7"):
		return modelDefaults{
			maxTokens:                    128000,
			rejectsCustomSampling:        true,
			rejectsManualThinkingEnabled: true,
			supportsThinking:             true,
			supportsAdaptiveThinking:     true,
		}
	case strings.HasPrefix(id, "claude-opus-4-6"):
		return modelDefaults{
			maxTokens:                128000,
			supportsThinking:         true,
			supportsAdaptiveThinking: true,
		}
	case strings.HasPrefix(id, "claude-sonnet-4-6"):
		return modelDefaults{
			maxTokens:                64000,
			supportsThinking:         true,
			supportsAdaptiveThinking: true,
		}
	case strings.HasPrefix(id, "claude-haiku-4-5"):
		return modelDefaults{
			maxTokens:        64000,
			supportsThinking: true,
		}
	case strings.HasPrefix(id, "claude-sonnet-4-5"),
		strings.HasPrefix(id, "claude-sonnet-4"):
		return modelDefaults{
			maxTokens:        64000,
			supportsThinking: true,
		}
	case strings.HasPrefix(id, "claude-opus-4-5"),
		strings.HasPrefix(id, "claude-opus-4-1"),
		strings.HasPrefix(id, "claude-opus-4"):
		return modelDefaults{
			maxTokens:        32000,
			supportsThinking: true,
		}
	case strings.HasPrefix(id, "claude-3-7-sonnet"):
		return modelDefaults{
			// Default keeps the historical 4096 cap; callers asking
			// for >64k must opt into the output-128k beta explicitly
			// via the ChatRequest.MaxTokens field.
			maxTokens:        0,
			supportsThinking: true,
			betas:            nil,
		}
	}
	return modelDefaults{}
}

// applyModelConstraints adjusts params in-place to conform to the
// per-model contract for req.Model. The caller's intent in req is
// honoured where possible; fields rejected by the model are stripped,
// thinking modes that must be rewritten are rewritten, and per-model
// max_tokens defaults are filled in when the caller left MaxTokens at
// zero.
//
// Expected:
//   - params.Model is set; other fields may be at their zero value.
//   - req contains the caller-supplied hints (MaxTokens, Temperature,
//     TopP, TopK, ThinkingMode, ToolChoice).
//
// Returns:
//   - A non-nil error when the request is rejected by the per-model
//     contract (e.g. Opus 4.7 with manual thinking: enabled, or
//     thinking parameters that fail validation).
//
// Side effects:
//   - Mutates params.
func applyModelConstraints(
	params *anthropicAPI.MessageNewParams, req provider.ChatRequest,
) error {
	defs := resolveModelDefaults(req.Model)

	if err := applyMaxTokens(params, req, defs); err != nil {
		return err
	}
	if err := applySampling(params, req, defs); err != nil {
		return err
	}
	if err := applyThinking(params, req, defs); err != nil {
		return err
	}
	if err := applyToolChoice(params, req); err != nil {
		return err
	}
	return nil
}

// applyMaxTokens fills params.MaxTokens from req or per-model defaults.
//
// Expected:
//   - params is non-nil.
//
// Returns:
//   - nil; this helper does not validate.
//
// Side effects:
//   - Mutates params.MaxTokens.
func applyMaxTokens(
	params *anthropicAPI.MessageNewParams,
	req provider.ChatRequest,
	defs modelDefaults,
) error {
	switch {
	case req.MaxTokens > 0:
		params.MaxTokens = int64(req.MaxTokens)
	case defs.maxTokens > 0:
		params.MaxTokens = defs.maxTokens
	default:
		params.MaxTokens = defaultMaxTokens
	}
	return nil
}

// applySampling threads temperature/top_p/top_k onto params, stripping
// every field for models that reject custom sampling (Opus 4.7).
//
// Expected:
//   - params is non-nil.
//
// Returns:
//   - nil; non-default sampling on a strict model is silently dropped
//     rather than rejected, because this preserves the historical
//     default-temperature-0 contract for callers that do not
//     explicitly opt into Opus 4.7's adaptive sampling shape.
//
// Side effects:
//   - Mutates params.Temperature, TopP, TopK.
func applySampling(
	params *anthropicAPI.MessageNewParams,
	req provider.ChatRequest,
	defs modelDefaults,
) error {
	if defs.rejectsCustomSampling {
		// Opus 4.7: leave every sampling field zero (omitzero) so the
		// model uses its server-side defaults.
		return nil
	}

	if req.Temperature != nil {
		params.Temperature = anthropicAPI.Float(*req.Temperature)
	} else {
		// Preserve the historical default of 0 for back-compat with
		// every caller that does not set Temperature.
		params.Temperature = anthropicAPI.Float(0)
	}
	if req.TopP != nil {
		params.TopP = anthropicAPI.Float(*req.TopP)
	}
	if req.TopK != nil {
		params.TopK = anthropicAPI.Int(int64(*req.TopK))
	}
	return nil
}

// applyThinking parses ThinkingMode and writes the corresponding
// ThinkingConfig on params, validating constraints (budget >= 1024,
// budget < max_tokens, max_tokens != 0). Models that do not support
// extended thinking silently drop the request.
//
// Expected:
//   - params.MaxTokens is already populated by applyMaxTokens.
//
// Returns:
//   - errThinkingBudgetTooLow / errThinkingBudgetExceedsMax /
//     errThinkingMaxTokensZero on validation failure.
//   - errThinkingEnabledRejected when the model rejects manual
//     thinking: enabled (Opus 4.7).
//   - nil on success or when the field is empty.
//
// Side effects:
//   - Mutates params.Thinking.
func applyThinking(
	params *anthropicAPI.MessageNewParams,
	req provider.ChatRequest,
	defs modelDefaults,
) error {
	mode := strings.TrimSpace(req.ThinkingMode)
	if mode == "" {
		return nil
	}
	if !defs.supportsThinking {
		// Older models silently drop thinking — there is nothing to
		// validate and nothing to send.
		return nil
	}

	switch {
	case mode == "disabled":
		params.Thinking = anthropicAPI.ThinkingConfigParamUnion{
			OfDisabled: ptrThinkingDisabled(),
		}
		return nil
	case mode == "adaptive":
		if !defs.supportsAdaptiveThinking {
			// Fall back to enabled-with-default-budget on models that
			// do not recognise the adaptive variant.
			return setEnabledThinking(params, minThinkingBudgetTokens)
		}
		params.Thinking = anthropicAPI.ThinkingConfigParamUnion{
			OfAdaptive: ptrThinkingAdaptive(),
		}
		return nil
	case mode == "enabled":
		if defs.rejectsManualThinkingEnabled {
			return errThinkingEnabledRejected
		}
		return setEnabledThinking(params, minThinkingBudgetTokens)
	case strings.HasPrefix(mode, "enabled:"):
		if defs.rejectsManualThinkingEnabled {
			return errThinkingEnabledRejected
		}
		var budget int64
		if _, err := fmt.Sscanf(mode, "enabled:%d", &budget); err != nil {
			return fmt.Errorf("anthropic: invalid thinking mode %q: %w", mode, err)
		}
		return setEnabledThinking(params, budget)
	}
	return nil
}

// setEnabledThinking writes a ThinkingConfigEnabled on params after
// validating the budget against the API contract.
//
// Expected:
//   - params.MaxTokens is already populated.
//   - budget is the requested thinking.budget_tokens.
//
// Returns:
//   - nil on success.
//   - errThinkingBudgetTooLow / errThinkingBudgetExceedsMax /
//     errThinkingMaxTokensZero on validation failure.
//
// Side effects:
//   - Mutates params.Thinking.
func setEnabledThinking(
	params *anthropicAPI.MessageNewParams, budget int64,
) error {
	if params.MaxTokens == 0 {
		return errThinkingMaxTokensZero
	}
	if budget < minThinkingBudgetTokens {
		return errThinkingBudgetTooLow
	}
	if budget >= params.MaxTokens {
		return errThinkingBudgetExceedsMax
	}
	enabled := anthropicAPI.ThinkingConfigParamOfEnabled(budget)
	params.Thinking = enabled
	return nil
}

// applyToolChoice maps the caller's ToolChoice string onto the SDK's
// union and validates the {auto, none}-only contract that applies when
// thinking is on.
//
// Expected:
//   - params.Thinking has already been populated by applyThinking.
//
// Returns:
//   - errThinkingToolChoiceInvalid when thinking is on AND ToolChoice
//     is "any" or "tool:NAME".
//   - nil on success or when the field is empty.
//
// Side effects:
//   - Mutates params.ToolChoice.
func applyToolChoice(
	params *anthropicAPI.MessageNewParams, req provider.ChatRequest,
) error {
	choice := strings.TrimSpace(req.ToolChoice)
	if choice == "" {
		return nil
	}

	thinkingActive := isThinkingActive(params)

	switch {
	case choice == "auto":
		params.ToolChoice = anthropicAPI.ToolChoiceUnionParam{
			OfAuto: &anthropicAPI.ToolChoiceAutoParam{},
		}
		return nil
	case choice == "none":
		none := anthropicAPI.NewToolChoiceNoneParam()
		params.ToolChoice = anthropicAPI.ToolChoiceUnionParam{OfNone: &none}
		return nil
	case choice == "any":
		if thinkingActive {
			return errThinkingToolChoiceInvalid
		}
		params.ToolChoice = anthropicAPI.ToolChoiceUnionParam{
			OfAny: &anthropicAPI.ToolChoiceAnyParam{},
		}
		return nil
	case strings.HasPrefix(choice, "tool:"):
		if thinkingActive {
			return errThinkingToolChoiceInvalid
		}
		name := strings.TrimPrefix(choice, "tool:")
		if name == "" {
			return fmt.Errorf("anthropic: tool_choice tool: missing tool name")
		}
		params.ToolChoice = anthropicAPI.ToolChoiceUnionParam{
			OfTool: &anthropicAPI.ToolChoiceToolParam{Name: name},
		}
		return nil
	}
	return fmt.Errorf("anthropic: unrecognised tool_choice %q", choice)
}

// isThinkingActive reports whether params has any non-disabled
// thinking config currently set.
//
// Expected:
//   - params is non-nil.
//
// Returns:
//   - true when adaptive or enabled thinking is configured.
//
// Side effects:
//   - None.
func isThinkingActive(params *anthropicAPI.MessageNewParams) bool {
	return params.Thinking.OfEnabled != nil || params.Thinking.OfAdaptive != nil
}

// ptrThinkingDisabled returns a pointer to a fresh
// ThinkingConfigDisabledParam with its required Type set.
//
// Returns:
//   - A non-nil *ThinkingConfigDisabledParam.
//
// Side effects:
//   - None.
func ptrThinkingDisabled() *anthropicAPI.ThinkingConfigDisabledParam {
	d := anthropicAPI.NewThinkingConfigDisabledParam()
	return &d
}

// ptrThinkingAdaptive returns a pointer to a fresh
// ThinkingConfigAdaptiveParam. The Display field is intentionally left
// at its zero value: the SDK marshals zero as the model's default
// (`omitted` for Opus 4.7, `summarized` for the 4.6 family).
//
// Returns:
//   - A non-nil *ThinkingConfigAdaptiveParam.
//
// Side effects:
//   - None.
func ptrThinkingAdaptive() *anthropicAPI.ThinkingConfigAdaptiveParam {
	return &anthropicAPI.ThinkingConfigAdaptiveParam{}
}
