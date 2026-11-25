package zai

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/provider"
)

// errMalformedUsage is the fixed reason for a usage object that cannot be
// decoded: a decoder error would echo the (possibly huge) number literal.
var errMalformedUsage = errors.New("malformed usage")

// UnmarshalJSON decodes the usage object, replacing any decoder error (which
// would echo a number literal into the error text) with a fixed reason.
func (u *openAIUsage) UnmarshalJSON(data []byte) error {
	var wire openAIUsageWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return errMalformedUsage
	}
	*u = openAIUsage(wire)
	return nil
}

// neutral converts the usage object; a nil one (absent or JSON null) or one
// with no known count is not reported. Cached tokens are moved out of the input
// count so the neutral counts are disjoint; a cached count above the prompt
// count, a negative or an absurd count fails closed.
func (u *openAIUsage) neutral() (*provider.Usage, error) {
	if u == nil {
		return nil, nil
	}
	var cachedPtr *int64
	if u.PromptTokensDetails != nil {
		cachedPtr = u.PromptTokensDetails.CachedTokens
	}
	if u.PromptTokens == nil && u.CompletionTokens == nil && cachedPtr == nil {
		return nil, nil
	}
	var prompt, completion, cached int64
	if u.PromptTokens != nil {
		prompt = *u.PromptTokens
	}
	if u.CompletionTokens != nil {
		completion = *u.CompletionTokens
	}
	if cachedPtr != nil {
		cached = *cachedPtr
	}
	if cached > prompt {
		return nil, fmt.Errorf("zai: usage cached_tokens %d exceed prompt_tokens %d", cached, prompt)
	}
	out := provider.Usage{InputTokens: prompt - cached, OutputTokens: completion, CacheReadTokens: cached}
	if err := out.Validate(); err != nil {
		return nil, fmt.Errorf("zai: %w", err)
	}
	return &out, nil
}

// merge applies one raw usage object. Messages usage is cumulative, so every
// count is kept monotonic: a gateway's zero never lowers an earlier real count. A count it omits is
// kept. Absent or null usage, or one with no known count, changes nothing; a
// malformed, negative or absurd one fails closed.
func (a *usageAccumulator) merge(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var wire anthropicUsage
	if err := json.Unmarshal(raw, &wire); err != nil {
		return &ToolProtocolError{Reason: "malformed usage"}
	}
	next, reported, known := a.usage, a.usage, false
	for _, field := range []struct {
		wire     *int64
		to       *int64
		reported *int64
	}{
		{wire.InputTokens, &next.InputTokens, &reported.InputTokens},
		{wire.OutputTokens, &next.OutputTokens, &reported.OutputTokens},
		{wire.CacheCreation, &next.CacheWriteTokens, &reported.CacheWriteTokens},
		{wire.CacheReadTokens, &next.CacheReadTokens, &reported.CacheReadTokens},
	} {
		if field.wire == nil {
			continue
		}
		known, *field.reported = true, *field.wire
		if *field.wire > *field.to {
			*field.to = *field.wire
		}
	}
	if !known {
		return nil
	}
	if err := reported.Validate(); err != nil {
		return &ToolProtocolError{Reason: err.Error()}
	}
	a.usage, a.seen = next, true
	return nil
}

// result is the folded usage, nil when no usage object was seen.
func (a *usageAccumulator) result() *provider.Usage {
	if !a.seen {
		return nil
	}
	out := a.usage
	return &out
}
