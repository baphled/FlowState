package app

import (
	"encoding/json"

	"github.com/baphled/flowstate/internal/provider"
)

// ContextTokens is the context a turn occupied once it finished: the prompt
// (input + cache read + cache write) plus the output the model generated, which
// is part of the next prompt. The provider counts are disjoint, so the sum
// needs no correction. Context compaction compares this against the model's
// window.
func ContextTokens(u provider.Usage) int64 {
	return u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

// add folds one turn's usage into the totals; nil (not reported) adds nothing
// and is not counted. It reports whether the usage was counted.
func (t *UsageTotals) add(u *provider.Usage) bool {
	if u == nil {
		return false
	}
	t.InputTokens += u.InputTokens
	t.OutputTokens += u.OutputTokens
	t.CacheReadTokens += u.CacheReadTokens
	t.CacheWriteTokens += u.CacheWriteTokens
	t.UsageTurns++
	return true
}

// recordUsage folds one provider turn into the run's totals and, when the turn
// reported usage, makes it the run's last context size.
func (r *ExecLoopResult) recordUsage(u *provider.Usage) {
	if !r.Usage.add(u) {
		return
	}
	r.LastContextTokens = ContextTokens(*u)
	r.LastContextReported = true
}

// assistantMessagePayload is the assistant.message payload: the content and,
// only when the provider reported it, the turn's usage. An omitted field keeps
// the payload (and its hash) of an unreported turn what it always was.
func assistantMessagePayload(content string, u *provider.Usage) map[string]any {
	payload := map[string]any{"content": content}
	if u != nil {
		payload["usage"] = *u
	}
	return payload
}

// messageUsage reads the usage of an assistant.message payload; nil when the
// field is absent or the payload is not a JSON object, so old and odd logs
// read as not reported.
func messageUsage(payloadJSON string) *provider.Usage {
	var p struct {
		Usage *provider.Usage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &p); err != nil {
		return nil
	}
	return p.Usage
}

// summariseUsage computes the session usage from its events.
func summariseUsage(events []RecordedEvent) SessionUsage {
	var out SessionUsage
	for _, e := range events {
		if e.Type != TypeAssistantMessage {
			continue
		}
		out.TurnsTotal++
		if u := messageUsage(e.PayloadJSON); out.Totals.add(u) {
			out.LastContextTokens = ContextTokens(*u)
		}
	}
	return out
}
