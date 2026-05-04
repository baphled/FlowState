import { describe, expect, it } from 'vitest'
import { collapseToolPairs, groupContextTools, resolveAgentName } from './chatViewHelpers'
import type { Agent, Message } from '@/types'

const agents: Agent[] = [
  { id: 'planner', name: 'Planner', model: 'claude-opus-4.7' } as Agent,
  { id: 'senior', name: 'Senior Engineer', model: 'claude-opus-4.7' } as Agent,
]

function makeMessage(overrides: Partial<Message> = {}): Message {
  return {
    id: 'm1',
    role: 'assistant',
    content: 'hi',
    timestamp: '2026-05-03T00:00:00Z',
    ...overrides,
  }
}

describe('resolveAgentName', () => {
  it('returns the agent name when message.agentId matches a known agent', () => {
    const msg = makeMessage({ agentId: 'planner' })
    expect(resolveAgentName(msg, agents, '')).toBe('Planner')
  })

  it('falls back to the active agent when message has no agentId', () => {
    const msg = makeMessage({ agentId: undefined })
    expect(resolveAgentName(msg, agents, 'senior')).toBe('Senior Engineer')
  })

  it('returns undefined for user messages', () => {
    const msg = makeMessage({ role: 'user', agentId: 'planner' })
    expect(resolveAgentName(msg, agents, 'planner')).toBeUndefined()
  })

  it('returns undefined when no agent matches', () => {
    const msg = makeMessage({ agentId: 'unknown' })
    expect(resolveAgentName(msg, agents, '')).toBeUndefined()
  })

  it('returns undefined when there is no agentId and no active agent', () => {
    const msg = makeMessage({ agentId: undefined })
    expect(resolveAgentName(msg, agents, '')).toBeUndefined()
  })
})

function makeToolMessage(
  id: string,
  role: 'tool_call' | 'tool_result' | 'tool_error',
  toolName: string,
  overrides: Partial<Message> = {},
): Message {
  return {
    id,
    role,
    content: '',
    timestamp: '2026-05-03T00:00:00Z',
    toolName,
    ...overrides,
  }
}

describe('collapseToolPairs', () => {
  it('suppresses a tool_call when followed by a tool_result with the same toolName', () => {
    const messages: Message[] = [
      makeToolMessage('1', 'tool_call', 'write', { toolInput: '{"filePath":"a.ts"}' }),
      makeToolMessage('2', 'tool_result', 'write', { content: 'ok' }),
    ]
    const result = collapseToolPairs(messages)
    expect(result).toHaveLength(1)
    expect(result[0].role).toBe('tool_result')
    expect(result[0].toolName).toBe('write')
  })

  it('keeps a tool_call when no matching tool_result follows', () => {
    const messages: Message[] = [
      makeToolMessage('1', 'tool_call', 'write'),
    ]
    const result = collapseToolPairs(messages)
    expect(result).toHaveLength(1)
    expect(result[0].role).toBe('tool_call')
  })

  it('does not pair tool_call with a tool_result of a different toolName', () => {
    const messages: Message[] = [
      makeToolMessage('1', 'tool_call', 'write'),
      makeToolMessage('2', 'tool_result', 'read'),
    ]
    const result = collapseToolPairs(messages)
    expect(result).toHaveLength(2)
  })

  it('collapses interleaved pairs in order', () => {
    const messages: Message[] = [
      makeToolMessage('1', 'tool_call', 'write'),
      makeToolMessage('2', 'tool_result', 'write'),
      makeToolMessage('3', 'tool_call', 'read'),
      makeToolMessage('4', 'tool_result', 'read'),
    ]
    const result = collapseToolPairs(messages)
    expect(result.map((m) => m.role)).toEqual(['tool_result', 'tool_result'])
    expect(result.map((m) => m.toolName)).toEqual(['write', 'read'])
  })

  it('passes through non-tool messages unchanged', () => {
    const messages: Message[] = [
      makeMessage({ role: 'user', content: 'hello' }),
      makeToolMessage('2', 'tool_call', 'write'),
      makeToolMessage('3', 'tool_result', 'write'),
      makeMessage({ role: 'assistant', content: 'done' }),
    ]
    const result = collapseToolPairs(messages)
    expect(result).toHaveLength(3)
    expect(result.map((m) => m.role)).toEqual(['user', 'tool_result', 'assistant'])
  })

  it('keeps tool_error messages and does not pair them with tool_call', () => {
    const messages: Message[] = [
      makeToolMessage('1', 'tool_call', 'write'),
      makeToolMessage('2', 'tool_error', 'write'),
    ]
    const result = collapseToolPairs(messages)
    expect(result).toHaveLength(2)
    expect(result.map((m) => m.role)).toEqual(['tool_call', 'tool_error'])
  })

  it('returns an empty array for an empty input', () => {
    expect(collapseToolPairs([])).toEqual([])
  })
})

describe('groupContextTools', () => {
  it('returns flat array when no context tools are present', () => {
    const messages: Message[] = [
      makeMessage({ id: '1', content: 'hello' }),
      makeMessage({ id: '2', content: 'world' }),
    ]
    const result = groupContextTools(messages)
    expect(result).toEqual([
      { type: 'message', message: messages[0] },
      { type: 'message', message: messages[1] },
    ])
  })

  it('groups 2+ consecutive context tool_results', () => {
    const messages: Message[] = [
      makeToolMessage('1', 'tool_result', 'read'),
      makeToolMessage('2', 'tool_result', 'grep'),
      makeToolMessage('3', 'tool_result', 'read'),
    ]
    const result = groupContextTools(messages)
    expect(result).toHaveLength(1)
    expect(result[0]).toEqual({
      type: 'context-group',
      messages: messages,
      toolCounts: { read: 2, grep: 1 },
    })
  })

  it('does not group single context tool_result', () => {
    const messages: Message[] = [
      makeMessage({ id: '1', content: 'start' }),
      makeToolMessage('2', 'tool_result', 'read'),
      makeMessage({ id: '3', content: 'end' }),
    ]
    const result = groupContextTools(messages)
    expect(result).toHaveLength(3)
    expect(result[1]).toEqual({ type: 'message', message: messages[1] })
  })

  it('interrupts grouping by non-context tool', () => {
    const messages: Message[] = [
      makeToolMessage('1', 'tool_result', 'read'),
      makeToolMessage('2', 'tool_result', 'read'),
      makeToolMessage('3', 'tool_result', 'bash'),
      makeToolMessage('4', 'tool_result', 'read'),
      makeToolMessage('5', 'tool_result', 'read'),
    ]
    const result = groupContextTools(messages)
    expect(result).toHaveLength(3)
    expect(result[0].type).toBe('context-group')
    expect(result[1]).toEqual({ type: 'message', message: messages[2] })
    expect(result[2].type).toBe('context-group')
  })

  it('interrupts grouping by different role', () => {
    const messages: Message[] = [
      makeToolMessage('1', 'tool_result', 'read'),
      makeToolMessage('2', 'tool_result', 'read'),
      makeToolMessage('3', 'tool_call', 'read'),
      makeToolMessage('4', 'tool_result', 'read'),
      makeToolMessage('5', 'tool_result', 'read'),
    ]
    const result = groupContextTools(messages)
    expect(result).toHaveLength(3)
    expect(result[0].type).toBe('context-group')
    expect(result[1].type).toBe('message')
    expect(result[2].type).toBe('context-group')
  })

  it('handles empty array', () => {
    expect(groupContextTools([])).toEqual([])
  })

  it('groups all context tools', () => {
    const messages: Message[] = [
      makeToolMessage('1', 'tool_result', 'read'),
      makeToolMessage('2', 'tool_result', 'glob'),
    ]
    const result = groupContextTools(messages)
    expect(result).toHaveLength(1)
    expect(result[0].type).toBe('context-group')
  })
})
