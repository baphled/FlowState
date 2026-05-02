import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchAgents, fetchSessionMessages, fetchSessions } from '../api'
import { useChatStore } from './chatStore'

// Mock API functions
vi.mock('../api', () => ({
  fetchAgents: vi.fn(() => Promise.resolve([
    { id: 'agent-1', name: 'Agent One' },
    { id: 'agent-2', name: 'Agent Two' },
  ])),
  fetchSessions: vi.fn(() => Promise.resolve([
    { id: 'session-1', agent_id: 'agent-1', name: 'Session 1' },
    { id: 'session-2', agent_id: 'agent-2', name: 'Session 2' },
  ])),
  fetchSessionMessages: vi.fn((sessionId: string) => Promise.resolve([
    { id: 'msg-1', session_id: sessionId, content: 'Hello', sender: 'user' },
    { id: 'msg-2', session_id: sessionId, content: 'Hi', sender: 'agent' },
  ])),
}))

describe('chatStore - restoreStateFromBackend', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    setActivePinia(createPinia())
  })

  it('restores agent, session, and messages from backend', async () => {
    const store = useChatStore()
    await expect(store.restoreStateFromBackend()).resolves.not.toThrow()
    expect(vi.mocked(fetchAgents)).toHaveBeenCalledTimes(1)
    expect(vi.mocked(fetchSessions)).toHaveBeenCalledTimes(1)
    expect(vi.mocked(fetchSessionMessages)).toHaveBeenCalledWith('session-1')
    expect(store.availableAgents.length).toBeGreaterThan(0)
    expect(store.sessions.length).toBeGreaterThan(0)
    expect(store.currentSessionId).toBe(store.sessions[0].id)
    expect(store.agentId).toBe(store.sessions[0].agentId)
    expect(store.messages.map((message) => message.content)).toEqual(['Hello', 'Hi'])
  })
})
