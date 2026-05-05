import { describe, expect, it } from 'vitest'
import { sanitiseMessageContent } from './messageContentBackstop'

// Defensive backstop tests — these exist to ensure the May 2026 chat-UI
// leaks (session 2d8dc0ac) cannot resurface in the Vue chat bubble even
// if a stale backend or a pre-fix persisted session is loaded. The
// primary fix is on the backend (internal/streaming.IsControlEvent,
// internal/engine.UnwrapTaskResult, internal/engine.sanitiseTaskError);
// these tests cover the frontend safety net.

describe('sanitiseMessageContent (chat-UI leak backstop)', () => {
  describe('Leak A — harness JSON prefix', () => {
    it('strips a leading {"attempt":N,"maxRetries":M} payload', () => {
      const raw =
        '{"attempt":1,"maxRetries":1}All requirements clear. I have all three inputs loaded.'

      const result = sanitiseMessageContent(raw)

      expect(result.content).toBe('All requirements clear. I have all three inputs loaded.')
      expect(result.appliedFilter).toBe('harness-json-prefix')
    })

    it('tolerates whitespace inside the JSON shape', () => {
      const raw = '{"attempt": 2, "maxRetries": 3}Continuing.'
      const result = sanitiseMessageContent(raw)
      expect(result.content).toBe('Continuing.')
      expect(result.appliedFilter).toBe('harness-json-prefix')
    })

    it('does NOT strip a string that merely contains "attempt" later in the text', () => {
      const raw = 'Here is my best attempt at fixing the bug.'
      const result = sanitiseMessageContent(raw)
      expect(result.content).toBe(raw)
      expect(result.appliedFilter).toBe('')
    })
  })

  describe('Leak B — <task_result> wrapper', () => {
    it('strips the canonical wrapper that formatDelegationOutput emits', () => {
      const raw = '<task_result>\nthe sub-agent reply\n</task_result>'
      const result = sanitiseMessageContent(raw)
      expect(result.content).toBe('the sub-agent reply')
      expect(result.appliedFilter).toBe('task-result-wrapper')
    })

    it('preserves multi-line inner content verbatim', () => {
      const inner = 'first line\n\nsecond line\nthird line'
      const raw = `<task_result>\n${inner}\n</task_result>`
      const result = sanitiseMessageContent(raw)
      expect(result.content).toBe(inner)
      expect(result.appliedFilter).toBe('task-result-wrapper')
    })

    it('does not strip an inline mention of the marker', () => {
      const raw = 'the model wrote: <task_result> is a marker'
      const result = sanitiseMessageContent(raw)
      expect(result.content).toBe(raw)
      expect(result.appliedFilter).toBe('')
    })

    it('handles the combined wrapper-plus-harness-prefix case captured in session 2d8dc0ac msg 167', () => {
      const raw =
        '<task_result>\n{"attempt":1,"maxRetries":1}All requirements clear. I have all three inputs loaded.\n</task_result>'

      const result = sanitiseMessageContent(raw)

      expect(result.content).toBe('All requirements clear. I have all three inputs loaded.')
      expect(result.appliedFilter).toBe('task-result-wrapper')
    })
  })

  describe('Leak C — delegation failure JSON', () => {
    it('replaces the raw failure shape with a friendly fallback that surfaces the correlation id', () => {
      const raw = JSON.stringify({
        error: 'rate limited — please retry shortly',
        status: 'failed',
        task_id: '1703b416-a0eb-4cf3-be3b-e07cc226b7fe',
        correlation_id: '09eb121ad341722b',
      })

      const result = sanitiseMessageContent(raw)

      expect(result.appliedFilter).toBe('delegation-failure-json')
      expect(result.content).toContain('rate-limited')
      expect(result.content).toContain('09eb121ad341722b')
      expect(result.content).not.toContain('1703b416-a0eb-4cf3-be3b-e07cc226b7fe')
      expect(result.correlationId).toBe('09eb121ad341722b')
    })

    it('handles the pre-fix raw provider error shape from session 2d8dc0ac msg 231', () => {
      const raw =
        '{"error":"delegation stream error: provider github-copilot error [rate_limit HTTP 429]: POST \\"https://api.githubcopilot.com/chat/completions\\": 429 Too Many Requests ","status":"failed","task_id":"1703b416-a0eb-4cf3-be3b-e07cc226b7fe"}'

      const result = sanitiseMessageContent(raw)

      expect(result.appliedFilter).toBe('delegation-failure-json')
      expect(result.content).not.toContain('github-copilot')
      expect(result.content).not.toContain('githubcopilot.com')
      expect(result.content).not.toContain('HTTP 429')
      expect(result.content).toContain('rate-limited')
    })

    it('falls back to a generic safe message when the error category is unrecognised', () => {
      const raw = JSON.stringify({
        error: 'something internal exploded',
        status: 'failed',
        task_id: 'abc',
      })
      const result = sanitiseMessageContent(raw)
      expect(result.appliedFilter).toBe('delegation-failure-json')
      expect(result.content).toContain('Sub-task failed')
      expect(result.content).not.toContain('exploded')
    })

    it('does NOT match a tool result that merely contains an "error" field (must have all three discriminators)', () => {
      const raw = JSON.stringify({ error: 'oops', status: 'completed', result: 'done' })
      const result = sanitiseMessageContent(raw)
      expect(result.appliedFilter).toBe('')
      expect(result.content).toBe(raw)
    })
  })

  describe('non-leak content is passed through unchanged', () => {
    it('returns plain text verbatim', () => {
      const result = sanitiseMessageContent('Hello, world!')
      expect(result.content).toBe('Hello, world!')
      expect(result.appliedFilter).toBe('')
    })

    it('returns valid JSON tool output verbatim', () => {
      const raw = '{"items":[1,2,3]}'
      const result = sanitiseMessageContent(raw)
      expect(result.content).toBe(raw)
      expect(result.appliedFilter).toBe('')
    })

    it('handles empty input safely', () => {
      const result = sanitiseMessageContent('')
      expect(result.content).toBe('')
      expect(result.appliedFilter).toBe('')
    })
  })
})
