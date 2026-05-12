// Package provider — agnostic attachment helpers used by every provider
// translator at the engine seam.
//
// The translator-helper layout (plan "Chat Attachments Backend (May 2026)"
// §6 task-10) keeps the engine boundary pure-Go-data:
//
//   - The agnostic Attachment struct lives in types.go.
//   - This file owns the per-request size ceiling and the sum helper that
//     every provider's request-builder gate calls before encoding to its
//     own SDK content-block shape.
//   - Each provider package (anthropic, openaicompat, ...) owns its own
//     attachments-to-content-block translator that consumes the agnostic
//     []Attachment slice and emits its native SDK type. SDK types do not
//     leak across the engine boundary (memory project_flowstate_engine_boundary).
//
// PR1 shipped the constants and helpers inline in internal/provider/anthropic/
// (commit ec015a86). PR3 lifts them here so the OpenAI / openaicompat /
// Copilot / ollamacloud / zai translators share the same ceiling without
// having to import the Anthropic package.
package provider

import "errors"

// MaxAttachmentRequestBytesValue is the per-request raw-byte ceiling for
// the total attachment payload threaded onto a single user turn across
// every provider. Anthropic's published hard cap on request body is
// ~32 MB; the 25 MB target ceiling here gives headroom for system
// prompt + text content + tool schemas. OpenAI / openaicompat callers
// share the same budget so a session that fits Anthropic also fits the
// alternate providers.
//
// Plan §6 task-04 acceptance criteria; lifted in PR3 task-10 from the
// Anthropic package to the shared seam.
const MaxAttachmentRequestBytesValue = int64(25 * 1024 * 1024)

// ErrAttachmentRequestTooLarge fires when the sum of attachment byte
// counts on a single turn exceeds MaxAttachmentRequestBytesValue.
// Bubbled out of every provider's request-build path so the engine
// surfaces a typed error to the UI.
//
// The sentinel is unwrapped via errors.Is by callers regardless of which
// provider raised it (Anthropic wraps with a %w on the wire, OpenAI /
// openaicompat callers do the same).
var ErrAttachmentRequestTooLarge = errors.New("attachments exceed per-request size ceiling")

// TotalAttachmentBytes reports the sum of Data byte counts across a
// slice of attachments. Exposed so each provider's engine-side
// request-size gate can pre-check against the 25 MB ceiling before any
// base64 encoding happens (base64 inflates by ~33%, so a 25 MB raw
// budget translates to ~33 MB on the wire — still under Anthropic's
// 32 MB hard cap with headroom for surrounding payload).
//
// Expected:
//   - atts may be nil or empty (returns 0).
//   - Each Attachment's Data slice carries the raw bytes (NOT
//     base64-encoded).
//
// Returns:
//   - The summed byte count.
//
// Side effects:
//   - None.
func TotalAttachmentBytes(atts []Attachment) int64 {
	var total int64
	for _, a := range atts {
		total += int64(len(a.Data))
	}
	return total
}

// MaxAttachmentRequestBytes returns the per-request raw-byte ceiling.
// Exposed as a function so tests can reference the value via the
// public API rather than depending on the constant directly.
//
// Returns:
//   - MaxAttachmentRequestBytesValue.
//
// Side effects:
//   - None.
func MaxAttachmentRequestBytes() int64 { return MaxAttachmentRequestBytesValue }
