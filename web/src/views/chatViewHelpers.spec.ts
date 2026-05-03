import { describe, expect, it } from 'vitest'
import { resolveAgentName } from './chatViewHelpers'
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
