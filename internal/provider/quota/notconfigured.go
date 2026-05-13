package quota

import (
	"context"
	"net/http"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// NotConfiguredAdapter is the canonical adapter for providers that
// ship NotConfigured for the v1 quota surface — either temporarily
// ("awaiting-pr3" for the openaicompat-inheriting providers whose
// success-path lift is PR3 territory) or permanently
// ("subscription-only" for copilot, "local-model" for ollama).
//
// The adapter satisfies the Quota interface (Remaining / RecordResponse)
// with a fixed Snapshot. RecordResponse is a no-op — the provider
// emits no quota signal FlowState can usefully record. This is the
// honest-stance fallback per plan §"Per-provider fidelity matrix"
// lines 141-149 and §"Architecture" lines 230 ("each provider gets
// a quota.go").
//
// Per memory project_flowstate_eventlogger_catalog_subscriber_is_dead
// _comment: the adapter being trivially uniform means a future reader
// can audit "is this provider really not configured?" via grep
// without diving into per-provider _quota.go files.
type NotConfiguredAdapter struct {
	providerID  string
	accountHash string
	reason      string
}

// NewNotConfiguredAdapter constructs an adapter that surfaces a
// NotConfigured Snapshot with the given Reason for every call.
//
// reason MUST be one of the documented strings at line 207 of the
// plan + per-provider matrix:
//   - "local-model"
//   - "no-quota-headers"
//   - "subscription-only"
//   - "awaiting-pr3"
//   - "unknown-model:<id>"
//
// Other strings compile but break the chip's tooltip rendering.
func NewNotConfiguredAdapter(providerID, accountHash, reason string) *NotConfiguredAdapter {
	return &NotConfiguredAdapter{
		providerID:  providerID,
		accountHash: accountHash,
		reason:      reason,
	}
}

// Remaining returns a NotConfigured Snapshot with the adapter's
// configured reason. The Snapshot satisfies IsValid() == true.
func (a *NotConfiguredAdapter) Remaining(_ context.Context, _, modelID string) (Snapshot, error) {
	return Snapshot{
		Provider:    a.providerID,
		AccountHash: a.accountHash,
		Model:       modelID,
		ObservedAt:  time.Now(),
		NotConfigured: &NotConfiguredVariant{
			Reason: a.reason,
		},
	}, nil
}

// RecordResponse is a no-op. NotConfigured providers emit no quota
// signal FlowState can record. Reserved for future provider behaviour
// changes (e.g. ollamacloud starts emitting headers and graduates
// out of NotConfigured) — the seam exists so the engine can fan out
// uniformly across all providers without nil-checking.
func (a *NotConfiguredAdapter) RecordResponse(_, _ string, _ http.Header, _ provider.Usage) {
}

// Compile-time conformance.
var _ Quota = (*NotConfiguredAdapter)(nil)
