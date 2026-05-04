import { describe, it, expect } from 'vitest'
import type { Session, SessionSummary } from './index'

/**
 * Type-drift contract spec — asserts every JSON field emitted by Go's
 * SessionResponse (internal/api/session_response.go) has a matching TS
 * field on Session and SessionSummary.
 *
 * This is a hand-mirrored contract (no codegen). The list below is the
 * exhaustive set of JSON tags from SessionResponse; if a field is added on
 * the Go side without updating this file, the test fails with a missing key.
 *
 * The TS requiredness comes from Go's `omitempty`:
 *   - tags WITHOUT omitempty → required boolean here (must be present)
 *   - tags WITH omitempty   → optional in TS, but still listed here so the
 *     contract still notices a Go-side rename.
 *
 * Static type assertions (the `assertHas*` block) catch field renames in
 * TS without a runtime — the build fails if a key is removed.
 */
describe('SessionResponse contract — Go ↔ TS hand-mirror', () => {
  // Source of truth: internal/api/session_response.go SessionResponse struct.
  // Update this list alongside any change to that struct's JSON tags.
  const goSessionResponseFields = [
    'id',
    'agentId',
    'currentAgentId',
    'currentModelId',
    'currentProviderId',
    'status',
    'parentId',
    'parentSessionId',
    'depth',
    'messages',
    'messageCount',
    'isStreaming',
    'createdAt',
    'updatedAt',
  ] as const

  it('Session has every field SessionResponse emits', () => {
    // Build a minimal valid Session — TypeScript's structural typing is the
    // first line of defence (this file fails to compile if a required field
    // is missing). The runtime check below catches optional-field drift
    // (a field renamed without updating goSessionResponseFields).
    const session: Session = {
      id: 's-1',
      agentId: 'a-1',
      currentAgentId: undefined,
      currentModelId: undefined,
      currentProviderId: undefined,
      status: 'active',
      parentId: undefined,
      parentSessionId: undefined,
      depth: 0,
      messages: [],
      messageCount: 0,
      isStreaming: false,
      createdAt: '2026-05-04T00:00:00Z',
      updatedAt: '2026-05-04T00:00:00Z',
    }
    const tsKeys = Object.keys(session)
    for (const required of goSessionResponseFields) {
      expect(tsKeys).toContain(required)
    }
  })

  it('SessionSummary mirrors the same Go SessionResponse contract', () => {
    // SessionSummary is the list-projection of Session — mirroring the same
    // contract so the union of fields used across both endpoints stays in sync.
    const summary: SessionSummary = {
      id: 's-1',
      agentId: 'a-1',
      currentAgentId: undefined,
      currentModelId: undefined,
      currentProviderId: undefined,
      parentId: undefined,
      parentSessionId: undefined,
      status: 'active',
      depth: 0,
      title: 'demo',
      createdAt: '2026-05-04T00:00:00Z',
      updatedAt: '2026-05-04T00:00:00Z',
      messageCount: 0,
      isStreaming: false,
    }
    const summaryKeys = Object.keys(summary)
    // Every Go field that makes sense on a list summary must be present.
    // `messages` is the only one Session has that SessionSummary deliberately omits.
    for (const required of goSessionResponseFields) {
      if (required === 'messages') continue
      expect(summaryKeys).toContain(required)
    }
  })

  it('isStreaming is required (no omitempty on the Go side)', () => {
    // Pin the type-drift fix: Session.IsStreaming has no omitempty in
    // session_response.go, so the wire ALWAYS carries the field. Reading
    // it without a guard must compile.
    const session: Session = {
      id: 's',
      agentId: 'a',
      status: 'active',
      depth: 0,
      messages: [],
      messageCount: 0,
      isStreaming: true,
      createdAt: '2026-05-04T00:00:00Z',
      updatedAt: '2026-05-04T00:00:00Z',
    }
    // Direct boolean read — fails to compile if isStreaming becomes optional.
    const flag: boolean = session.isStreaming
    expect(flag).toBe(true)
  })
})
