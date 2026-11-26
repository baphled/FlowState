// Package app — sessiondoctor.go owns the read-only session doctor
// use case: one aggregation of existing primitives into a
// SessionDiagnosis for the CLI surface. It performs no writes and no
// recovery; the crash-recovery mark stays in the run preamble
// (ADR-006).
package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// Caps on the untrusted text a hook event carries (ADR-012 Security): the loader's hook id
// length, and a short bound for a scope, a point or a digest. A reason is bounded by
// tool.HookReasonCap, as the runner bounds it.
const (
	doctorHookIDCap = 64
	doctorFieldCap  = 128
)

// DiagnoseSession aggregates existing primitives into one read-only
// diagnosis: st.GetSession (raw status; errors pass through
// unchanged so an unknown id stays session_not_found, exit 1 — it
// must NOT be wrapped in ErrUsage, which ErrorCode matches first),
// DetectCrashedSession, the run-activity predicate, the
// BudgetOverview snapshot, the pending steering-message count and the
// last turn.end's finish reason.
// Crashed is additionally gated on an active row status: only a
// session the engine could still resume is reported as crashed.
func DiagnoseSession(ctx context.Context, st *Store, sessionID string) (SessionDiagnosis, error) {
	return DiagnoseSessionWith(ctx, st, sessionID, CompactionConfig{})
}

// DiagnoseSessionWith is DiagnoseSession that also resolves the session's
// context window with cfg (ADR-010, D3): a root session has no profile, and the
// swarm coordinator's profile is not known here, so the window comes from
// cfg.ContextWindow, else the registry table. An unknown window adds a warning.
// The log's hook events are summarised as data (ADR-012, packet 4d); a log without
// one leaves Hooks nil.
func DiagnoseSessionWith(ctx context.Context, st *Store, sessionID string, cfg CompactionConfig) (SessionDiagnosis, error) {
	sess, err := st.GetSession(ctx, sessionID)
	if err != nil {
		return SessionDiagnosis{}, err
	}
	events, err := st.Replay(ctx, sessionID)
	if err != nil {
		return SessionDiagnosis{}, fmt.Errorf("doctor replay: %w", err)
	}
	crashed := crashedFromEvents(events)
	activity := hasRunActivity(events)
	snap, err := st.BudgetSnapshot(ctx, sessionID)
	if err != nil {
		return SessionDiagnosis{}, fmt.Errorf("doctor budget snapshot: %w", err)
	}
	pending, err := st.PendingSteeringMessages(ctx, sessionID)
	if err != nil {
		return SessionDiagnosis{}, fmt.Errorf("doctor pending steering messages: %w", err)
	}
	finish := lastFinishReason(events)
	liveCrash := crashed && sess.Status == "active"
	tokens, source := ResolveContextWindow(sess.Provider, sess.ModelID, 0, cfg)
	hooks, _ := summariseHooks(events)
	return SessionDiagnosis{
		ContextWindow:   ContextWindow{Tokens: tokens, Source: source},
		Warnings:        windowWarnings(source),
		SessionID:       sess.ID,
		Status:          sess.Status,
		Crashed:         liveCrash,
		FinishReason:    finish,
		Truncated:       FinishTruncated(finish) && !liveCrash && truncationStatus(sess.Status),
		RunActivity:     activity,
		PendingMessages: len(pending),
		Reserved:        snap.Reserved,
		Pool:            snap.Pool,
		Available:       snap.Available,
		Usage:           summariseUsage(events),
		Compactions:     summariseCompactions(events),
		Hooks:           hooks,
	}, nil
}

// summariseHooks reads the log's hook events as data (ADR-012 D9: replay never runs a hook) into the
// doctor's hook summary. The bool is false for a log without a hook event, so the doctor's output is
// unchanged for a session that never ran a hook.
func summariseHooks(events []store.RecordedEvent) (*HookStats, bool) {
	s := &HookStats{Unmatched: []HookInvocation{}, Denials: []HookDenial{}}
	pending := map[string]HookInvocation{} // invoked, no result yet, by hook id
	index := map[string]int{}              // a denying point and hook id -> its Denials entry
	seen := false
	for _, e := range events {
		p := payloadMap(e.PayloadJSON)
		switch e.Type {
		case TypeHooksLoaded:
			seen = true
			s.Loaded = hookLoadedSummary(p)
		case TypeHookInvoked:
			seen = true
			id := doctorText(payloadField(p, "id"), doctorHookIDCap)
			if prior, ok := pending[id]; ok {
				s.Unmatched = append(s.Unmatched, prior) // invoked again before its result: the prior one is unmatched
			}
			pending[id] = HookInvocation{Point: doctorText(payloadField(p, "point"), doctorFieldCap), ID: id, Seq: e.Seq}
		case TypeHookResult:
			seen = true
			id := doctorText(payloadField(p, "id"), doctorHookIDCap)
			delete(pending, id)
			recordHookResult(s, index, p, id)
		}
	}
	if !seen {
		return nil, false
	}
	for _, inv := range pending {
		s.Unmatched = append(s.Unmatched, inv) // open at the log's end
	}
	slices.SortFunc(s.Unmatched, func(a, b HookInvocation) int { return cmp.Compare(a.Seq, b.Seq) })
	return s, true
}

// recordHookResult tallies one hook.result: a deny is a denial of that hook with the last reason
// recorded, failed a failure, a stop the hook_stop outcome and a user.prompt refusal the hook_refused
// outcome (the last outcome in the log wins). A refusal is a deny, with its reason, or a failure under
// on_failure deny, which ends the run as hook_refused too (ADR-012 D6) and reads the failure code as its
// reason; it stays counted in Failed.
func recordHookResult(s *HookStats, index map[string]int, p map[string]any, id string) {
	point := doctorText(payloadField(p, "point"), doctorFieldCap)
	atPrompt := point == string(HookUserPrompt)
	switch payloadField(p, "decision") {
	case "deny":
		reason := doctorText(payloadField(p, "reason"), tool.HookReasonCap)
		key := point + "\x00" + id
		if i, ok := index[key]; ok {
			s.Denials[i].Count++
			s.Denials[i].Reason = reason
		} else {
			index[key] = len(s.Denials)
			s.Denials = append(s.Denials, HookDenial{Point: point, ID: id, Count: 1, Reason: reason})
		}
		if atPrompt {
			s.Outcome = &HookOutcome{Code: CodeHookRefused, ID: id, Reason: reason}
		}
	case "failed":
		s.Failed++
		if atPrompt && payloadField(p, "on_failure") == HookOnFailureDeny {
			failure := doctorText(payloadField(p, "failure"), doctorFieldCap)
			s.Outcome = &HookOutcome{Code: CodeHookRefused, ID: id, Reason: failure}
		}
	}
	if stopped, _ := p["stop"].(bool); stopped {
		s.Outcome = &HookOutcome{Code: CodeHookStop, ID: id}
	}
}

// hookLoadedSummary reads one hooks.loaded payload. A payload that is not an object yields
// empty lists, as the doctor must not fail on an odd log.
func hookLoadedSummary(p map[string]any) *HookLoaded {
	l := &HookLoaded{Scope: doctorText(payloadField(p, "scope"), doctorFieldCap),
		SetSHA256: doctorText(payloadField(p, "set_sha256"), doctorFieldCap),
		IDs:       map[string][]string{},
		Skipped:   []HookSkipped{}, Inert: []string{}}
	for _, item := range payloadList(p, "hooks") {
		if h, ok := item.(map[string]any); ok {
			source := doctorText(payloadField(h, "source"), doctorFieldCap)
			l.IDs[source] = append(l.IDs[source], doctorText(payloadField(h, "id"), doctorHookIDCap))
		}
	}
	for _, item := range payloadList(p, "skipped") {
		if sk, ok := item.(map[string]any); ok {
			l.Skipped = append(l.Skipped, HookSkipped{ID: doctorText(payloadField(sk, "id"), doctorHookIDCap),
				Reason: doctorText(payloadField(sk, "reason"), doctorFieldCap)})
		}
	}
	for _, item := range payloadList(p, "inert") {
		if id, ok := item.(string); ok {
			l.Inert = append(l.Inert, doctorText(id, doctorHookIDCap))
		}
	}
	return l
}

// payloadField returns a payload field's text; a missing field or one that is not a string reads as "".
func payloadField(p map[string]any, field string) string {
	text, _ := p[field].(string)
	return text
}

// payloadList returns a payload field's items; a missing field or one that is not an array reads as none.
func payloadList(p map[string]any, field string) []any {
	list, _ := p[field].([]any)
	return list
}

// doctorText makes text read from a hook event safe to print on either surface: the runner's sanitiser
// (tool.SanitizeHookText) removes every control, format and line-separator character — the class a hook
// result is stripped of when it is written (ADR-012 Security) — so an older or hand-written log cannot paint
// the operator's terminal, and the text is cut to limit bytes at a character boundary. The CLI's terminal-safe
// JSON escapes what remains again; the HTTP body carries the text as it is, which is why the stripping
// happens here.
func doctorText(text string, limit int) string {
	return truncateText(tool.SanitizeHookText(text, false), limit)
}

// hasRunActivity reports whether the events hold provider.request,
// tool.call or assistant.delta events — the same run-activity
// evidence the crash predicate applies to tell a crashed run from a
// freshly created session.
func hasRunActivity(events []RecordedEvent) bool {
	for _, e := range events {
		if e.Type == "provider.request" || e.Type == "tool.call" || e.Type == TypeAssistantDelta {
			return true
		}
	}
	return false
}

// FinishTruncated reports whether a turn's finish reason says the
// answer was cut off at the token limit (provider.FinishLength).
func FinishTruncated(reason string) bool {
	return reason == provider.FinishLength
}

// truncationStatus reports whether a row status may carry a truncated
// final turn: failed, cancelled and paused sessions never do.
func truncationStatus(status string) bool {
	return status == "active" || status == "completed"
}

// lastFinishReason returns the finish_reason of the LAST turn.end
// event, "" when there is none, the reason is absent, or the payload
// is not a JSON object with a string reason — the doctor is
// read-only and must not fail on old or odd logs.
func lastFinishReason(events []RecordedEvent) string {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == "turn.end" {
			return payloadFieldText(events[i].PayloadJSON, "finish_reason")
		}
	}
	return ""
}

// windowWarnings returns the doctor warnings for a window source: only an
// unknown window warns, and the text names the fix.
func windowWarnings(source string) []string {
	if source != WindowSourceUnknown {
		return nil
	}
	return []string{"no context window is known for this model; set compaction.context_window in the config " +
		"(automatic compaction will need it)"}
}
