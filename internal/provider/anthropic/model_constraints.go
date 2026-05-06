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
//
// The errAssistantPrefillRejected sentinel is returned when the
// request's last message has role "assistant" (an "assistant prefill")
// and the target model rejects prefill (Opus 4.6/4.7, Sonnet 4.6).
// Without this pre-flight check the server returns an opaque HTTP 400
// with body `Prefilling assistant messages is not supported for this
// model`.
var (
	errThinkingBudgetTooLow      = errors.New("anthropic: thinking budget_tokens must be >= 1024")
	errThinkingBudgetExceedsMax  = errors.New("anthropic: thinking budget_tokens must be < max_tokens")
	errThinkingMaxTokensZero     = errors.New("anthropic: thinking requires max_tokens > 0")
	errThinkingToolChoiceInvalid = errors.New("anthropic: thinking only supports tool_choice in {auto, none}")
	errThinkingEnabledRejected   = errors.New("anthropic: model rejects manual thinking: enabled (use adaptive)")
	errAssistantPrefillRejected  = errors.New("anthropic: model does not support assistant prefill")
)

// minThinkingBudgetTokens is the API-enforced minimum for
// thinking.budget_tokens when the field is set.
const minThinkingBudgetTokens int64 = 1024

// interleavedThinkingBetaHeader is the anthropic-beta value that opts a
// request into interleaved thinking — the model is allowed to emit
// further thinking blocks between successive tool_use blocks within a
// single turn. Without this header on Claude 4.x models that need it,
// thinking happens once at the top of the turn and tool use proceeds
// without further thinking, degrading multi-step reasoning.
//
// Claude 4.6+ family (Opus 4.6/4.7, Sonnet 4.6) auto-enables
// interleaving server-side when adaptive thinking is on; sending the
// header is a no-op on the direct API but is REJECTED on the Bedrock
// and Vertex passthroughs, so it must NOT be sent for those models.
//
// Sonnet 3.7 does not support interleaving at all. Claude 3.5/3.0
// have no thinking and so the header is never emitted.
const interleavedThinkingBetaHeader = "interleaved-thinking-2025-05-14"

// tokenEfficientToolsBetaHeader is the anthropic-beta value that opts a
// Sonnet 3.7 request into the token-efficient tool-use shape. The
// header reduces output tokens on tool-using turns by ~14-70% and is
// harmless on turns where the model does not actually emit a tool_use
// block, so the gate is simply "tools are declared on this request".
//
// Only Sonnet 3.7 honours this beta. On Claude 4+ the header is
// silently ignored on the direct API but we strip it for request
// cleanliness — the matrix in resolveModelDefaults pins which model
// families opt in.
const tokenEfficientToolsBetaHeader = "token-efficient-tools-2025-02-19"

// output128kBetaHeader is the anthropic-beta value that lifts Sonnet
// 3.7's default 64k max_tokens cap to 128k. The header is opt-in: the
// caller signals intent by setting MaxTokens above the post-beta
// threshold, and we transparently add the header so the upstream
// request is accepted instead of clamped.
//
// Only Sonnet 3.7 honours this beta. Claude 4+ models have their own
// per-model max_tokens ceilings and do not need (and silently ignore)
// this header — we strip it so the wire request matches the model.
const output128kBetaHeader = "output-128k-2025-02-19"

// output128kThreshold is the caller-requested max_tokens value above
// which Sonnet 3.7 needs the output-128k-2025-02-19 beta header for
// the request to be accepted. 64000 is the post-beta cap; anything
// strictly greater forces the opt-in.
const output128kThreshold int64 = 64000

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
	// requiresInterleavedThinkingHeader reports that the model needs
	// the explicit `interleaved-thinking-2025-05-14` anthropic-beta
	// header to interleave thinking with tool_use within a turn.
	// Claude 4 family (Opus 4/4.1/4.5, Sonnet 4/4.5, Haiku 4.5) needs
	// it; the 4.6+ family (Opus 4.6/4.7, Sonnet 4.6) auto-enables
	// interleaving server-side and rejects the explicit header on
	// Bedrock/Vertex; Sonnet 3.7 does not support interleaving.
	requiresInterleavedThinkingHeader bool
	// rejectsAssistantPrefill reports that the model rejects requests
	// whose final message has role "assistant" (an "assistant prefill"
	// — used by some callers to force an output prefix such as "{").
	// The 4.6+ family (Opus 4.6/4.7, Sonnet 4.6) returns HTTP 400 in
	// that case; older Claude models (4.5 and below, Sonnet 3.7, 3.5,
	// 3.0) accept prefill.
	rejectsAssistantPrefill bool
	// supportsTokenEfficientToolsBeta reports that the model honours
	// the `token-efficient-tools-2025-02-19` anthropic-beta header.
	// Only Sonnet 3.7 does. The header is auto-injected when tools are
	// present on the request, regardless of whether the model actually
	// uses one — the beta is harmless on tool-less turns.
	supportsTokenEfficientToolsBeta bool
	// supportsOutput128kBeta reports that the model honours the
	// `output-128k-2025-02-19` anthropic-beta header. Only Sonnet 3.7
	// does. The header is auto-injected when the caller asks for
	// max_tokens above the post-beta threshold (64k); below that the
	// header is unnecessary so we omit it.
	supportsOutput128kBeta bool
	// betas are anthropic-beta header values to add unconditionally
	// when the caller is on this model. Currently empty for every
	// supported model — the conditional Sonnet 3.7 betas are gated by
	// the supports*Beta flags above instead so the request only opts
	// in when the caller's intent matches the beta.
	betas []string
}

// betaHeaders returns the per-call anthropic-beta header values that
// must be added to the request, given whether thinking is on for this
// turn, whether tools are present, and the resolved max_tokens.
//
// The interleaved-thinking header is emitted IFF the model needs it
// AND thinking is on AND tools are present — without all three the
// header is either harmful (rejected on Bedrock/Vertex) or pointless.
//
// The Sonnet 3.7 token-efficient-tools header is emitted IFF the
// model honours it AND tools are present on the request — the beta is
// harmless on tool-less turns but we keep the request clean.
//
// The Sonnet 3.7 output-128k header is emitted IFF the model honours
// it AND the caller asked for max_tokens above the post-beta threshold
// (64k). Below that the header is unnecessary; above it the header is
// what makes the larger budget legal on the wire.
//
// Static per-model betas (the `betas` field) are appended afterwards
// so callers always get a single ordered slice; currently empty for
// every supported model.
//
// Expected:
//   - thinkingOn is true when params.Thinking is adaptive or enabled.
//   - toolsPresent is true when the request will include any tool
//     definitions on the wire.
//   - maxTokens is the resolved params.MaxTokens for this request
//     (already populated by applyMaxTokens; never zero on the hot
//     path because applyMaxTokens fills the package-wide default).
//
// Returns:
//   - The beta header values to add, in order. Empty/nil means no
//     per-call beta header should be sent.
//
// Side effects:
//   - None.
func (d modelDefaults) betaHeaders(
	thinkingOn, toolsPresent bool, maxTokens int64,
) []string {
	var headers []string
	if d.requiresInterleavedThinkingHeader && thinkingOn && toolsPresent {
		headers = append(headers, interleavedThinkingBetaHeader)
	}
	if d.supportsTokenEfficientToolsBeta && toolsPresent {
		headers = append(headers, tokenEfficientToolsBetaHeader)
	}
	if d.supportsOutput128kBeta && maxTokens > output128kThreshold {
		headers = append(headers, output128kBetaHeader)
	}
	if len(d.betas) > 0 {
		headers = append(headers, d.betas...)
	}
	return headers
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
			rejectsAssistantPrefill:      true,
		}
	case strings.HasPrefix(id, "claude-opus-4-6"):
		return modelDefaults{
			maxTokens:                128000,
			supportsThinking:         true,
			supportsAdaptiveThinking: true,
			rejectsAssistantPrefill:  true,
		}
	case strings.HasPrefix(id, "claude-sonnet-4-6"):
		return modelDefaults{
			maxTokens:                64000,
			supportsThinking:         true,
			supportsAdaptiveThinking: true,
			rejectsAssistantPrefill:  true,
		}
	case strings.HasPrefix(id, "claude-haiku-4-5"):
		return modelDefaults{
			maxTokens:                         64000,
			supportsThinking:                  true,
			requiresInterleavedThinkingHeader: true,
		}
	case strings.HasPrefix(id, "claude-sonnet-4-5"),
		strings.HasPrefix(id, "claude-sonnet-4"):
		return modelDefaults{
			maxTokens:                         64000,
			supportsThinking:                  true,
			requiresInterleavedThinkingHeader: true,
		}
	case strings.HasPrefix(id, "claude-opus-4-5"),
		strings.HasPrefix(id, "claude-opus-4-1"),
		strings.HasPrefix(id, "claude-opus-4"):
		return modelDefaults{
			maxTokens:                         32000,
			supportsThinking:                  true,
			requiresInterleavedThinkingHeader: true,
		}
	case strings.HasPrefix(id, "claude-3-7-sonnet"):
		return modelDefaults{
			// Default keeps the historical 4096 cap; callers asking
			// for >64k must opt into the output-128k beta explicitly
			// via the ChatRequest.MaxTokens field.
			maxTokens:                       0,
			supportsThinking:                true,
			supportsTokenEfficientToolsBeta: true,
			supportsOutput128kBeta:          true,
			betas:                           nil,
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

	if err := applyAssistantPrefill(req, defs); err != nil {
		return err
	}
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

// applyAssistantPrefill rejects requests whose final message has role
// "assistant" on models that do not support prefill (Opus 4.6/4.7,
// Sonnet 4.6). Without this pre-flight check the server returns HTTP
// 400 "Prefilling assistant messages is not supported for this model."
// — opaque from the caller's perspective. We surface a clear sentinel
// error wrapping the model id so callers can branch on
// errAssistantPrefillRejected and the model id is visible in logs.
//
// Expected:
//   - req.Messages may be empty, end with "user", or end with
//     "assistant"; only the final entry is inspected.
//
// Returns:
//   - errAssistantPrefillRejected (wrapped, with model id and detected
//     role for diagnostics) when the model rejects prefill AND the
//     final message has role "assistant".
//   - nil otherwise — including empty Messages and capable-model cases.
//
// Side effects:
//   - None. This validator is pure; it does not mutate params or req.
func applyAssistantPrefill(
	req provider.ChatRequest, defs modelDefaults,
) error {
	if !defs.rejectsAssistantPrefill {
		return nil
	}
	if len(req.Messages) == 0 {
		return nil
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != "assistant" {
		return nil
	}
	return fmt.Errorf(
		"%w: model %s does not support assistant prefill (last message was role=%s)",
		errAssistantPrefillRejected, req.Model, last.Role,
	)
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
