import { defineStore } from 'pinia'
import type { Agent, Message, SessionSummary } from '@/types'
import {
  createSession,
  fetchAgents,
  fetchSessionMessages,
  fetchSessions,
  sendSessionMessage,
  updateSessionAgent,
} from '@/api'

const activeSessionStorageKey = 'chat.currentSessionId'
const activeAgentStorageKey = 'chat.agentId'

function getPersistedSessionId(): string | null {
  if (typeof window === 'undefined') {
    return null
  }

  return window.localStorage.getItem(activeSessionStorageKey)
}

function persistSessionId(sessionId: string | null): void {
  if (typeof window === 'undefined') {
    return
  }

  if (sessionId) {
    window.localStorage.setItem(activeSessionStorageKey, sessionId)
    return
  }

  window.localStorage.removeItem(activeSessionStorageKey)
}

function getPersistedAgentId(): string | null {
  if (typeof window === 'undefined') {
    return null
  }

  return window.localStorage.getItem(activeAgentStorageKey)
}

function persistAgentId(agentId: string | null): void {
  if (typeof window === 'undefined') {
    return
  }

  if (agentId) {
    window.localStorage.setItem(activeAgentStorageKey, agentId)
    return
  }

  window.localStorage.removeItem(activeAgentStorageKey)
}


export const useChatStore = defineStore('chat', {
  state: () => ({
    availableAgentDetails: [] as Agent[],
    availableAgents: [] as string[],
    agentId: '',
    currentSessionId: null as string | null,
    sessions: [] as SessionSummary[],
    messages: [] as Message[],
    isLoading: false,
    isLoadingSessions: false,
    error: null as string | null,
  }),

  actions: {
    async restoreStateFromBackend(): Promise<void> {
      await this.loadAgents()
      await this.loadSessions()

      const persistedAgentId = getPersistedAgentId()
      const persistedSessionId = getPersistedSessionId()
      const session = this.sessions.find((item) => item.id === persistedSessionId)
      const agentId = session?.agentId ?? persistedAgentId ?? this.availableAgents[0] ?? ''

      this.agentId = agentId
      persistAgentId(agentId || null)

      if (!session || session.agentId !== agentId) {
        const sessionForAgent = this.sessions.find((item) => item.agentId === agentId)

        if (!sessionForAgent) {
          this.currentSessionId = null
          this.messages = []
          persistSessionId(null)
          return
        }

        this.currentSessionId = sessionForAgent.id
        persistSessionId(sessionForAgent.id)
        this.messages = await fetchSessionMessages(sessionForAgent.id)
        return
      }

      this.currentSessionId = session.id
      persistSessionId(session.id)
      this.messages = await fetchSessionMessages(session.id)
    },

    async loadAgents(): Promise<void> {
      const agents = await fetchAgents()
      this.availableAgentDetails = agents
      this.availableAgents = agents.map((agent) => agent.id)

      if (!this.agentId && agents.length > 0) {
        await this.setAgent(agents[0].id)
        return
      }

      if (this.agentId && !this.availableAgents.includes(this.agentId) && agents.length > 0) {
        await this.setAgent(agents[0].id)
      }
    },

    async setAgent(agentId: string): Promise<void> {
      const previousAgentId = this.agentId
      this.agentId = agentId
      persistAgentId(agentId)

      if (!agentId || !this.currentSessionId || agentId === previousAgentId) {
        return
      }

      try {
        await updateSessionAgent(this.currentSessionId, agentId)
      } catch (error) {
        this.error = error instanceof Error ? error.message : 'Failed to update session agent'
      }
    },

    getSelectedAgent(): Agent | undefined {
      return this.availableAgentDetails.find((agent) => agent.id === this.agentId)
    },

    clearMessages(): void {
      this.messages = []
      this.error = null
    },

    async loadSessions(): Promise<void> {
      this.isLoadingSessions = true
      try {
        this.sessions = await fetchSessions()
      } finally {
        this.isLoadingSessions = false
      }
    },

    async newSession(): Promise<void> {
      const session = await createSession(this.agentId)
      this.currentSessionId = session.id
      persistSessionId(session.id)
    },

    async loadSessionMessages(sessionId: string): Promise<void> {
      this.isLoading = true
      this.error = null
      try {
        const session = this.sessions.find((item) => item.id === sessionId)
        if (session && session.agentId !== this.agentId) {
          await this.setAgent(session.agentId)
        }

        this.messages = await fetchSessionMessages(sessionId)
        this.currentSessionId = sessionId
        persistSessionId(sessionId)
      } finally {
        this.isLoading = false
      }
    },

    async sendMessage(content: string): Promise<void> {
      const text = content.trim()
      if (!text || this.isLoading) {
        return
      }

      this.error = null
      this.isLoading = true
      let pollHandle: ReturnType<typeof setInterval> | null = null
      try {
        let sessionId = this.currentSessionId
        if (!sessionId) {
          const session = await createSession(this.agentId)
          sessionId = session.id
          this.currentSessionId = sessionId
          persistSessionId(sessionId)
        }

        const pollSessionId = sessionId
        let pollInFlight = false
        pollHandle = setInterval(() => {
          if (pollInFlight) {
            return
          }
          pollInFlight = true
          fetchSessionMessages(pollSessionId)
            .then((messages) => {
              if (this.currentSessionId === pollSessionId && this.isLoading) {
                this.messages = messages
              }
            })
            .catch(() => {})
            .finally(() => {
              pollInFlight = false
            })
        }, 1500)

        await sendSessionMessage(sessionId, text)
        this.messages = await fetchSessionMessages(sessionId)
        await this.loadSessions()
      } catch (error) {
        this.error = error instanceof Error ? error.message : 'Failed to send message'
      } finally {
        if (pollHandle !== null) {
          clearInterval(pollHandle)
        }
        this.isLoading = false
      }
    },
  },
})
