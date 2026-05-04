import { defineStore } from 'pinia'
import type { Agent, Message, Model, SessionSummary } from '@/types'
import {
  createSession,
  fetchAgents,
  fetchModels,
  fetchSessionMessages,
  fetchSessions,
  sendSessionMessage,
  subscribeSessionStream,
  updateSessionAgent,
  updateSessionModel,
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
    availableModels: [] as Model[],
    agentId: '',
    currentModelId: '',
    currentProviderId: '',
    currentSessionId: null as string | null,
    sessions: [] as SessionSummary[],
    messages: [] as Message[],
    isLoading: false,
    isStreaming: false,
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
      const sessionAgentId = session?.currentAgentId ?? session?.agentId
      const agentId = sessionAgentId ?? persistedAgentId ?? this.availableAgents[0] ?? ''

      this.agentId = agentId
      persistAgentId(agentId || null)

      if (!session || sessionAgentId !== agentId) {
        const sessionForAgent = this.sessions.find(
          (item) => (item.currentAgentId ?? item.agentId) === agentId,
        )

        if (!sessionForAgent) {
          this.currentSessionId = null
          this.messages = []
          this.currentModelId = ''
          this.currentProviderId = ''
          persistSessionId(null)
          return
        }

        this.currentSessionId = sessionForAgent.id
        this.currentModelId = sessionForAgent.currentModelId ?? ''
        this.currentProviderId = sessionForAgent.currentProviderId ?? ''
        persistSessionId(sessionForAgent.id)
        this.messages = await fetchSessionMessages(sessionForAgent.id)
        return
      }

      this.currentSessionId = session.id
      this.currentModelId = session.currentModelId ?? ''
      this.currentProviderId = session.currentProviderId ?? ''
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

    async setModel(modelId: string, providerId: string): Promise<void> {
      const previousModelId = this.currentModelId
      const previousProviderId = this.currentProviderId
      this.currentModelId = modelId
      this.currentProviderId = providerId

      if (!this.currentSessionId) {
        return
      }

      if (modelId === previousModelId && providerId === previousProviderId) {
        return
      }

      try {
        await updateSessionModel(this.currentSessionId, modelId, providerId)
      } catch (error) {
        this.error = error instanceof Error ? error.message : 'Failed to update session model'
      }
    },

    async loadModels(): Promise<void> {
      this.availableModels = await fetchModels()
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
        const sessionAgentId = session?.currentAgentId ?? session?.agentId

        // Switch the active session id BEFORE delegating to setAgent.
        // setAgent reads currentSessionId to decide which session to PATCH;
        // running it before this assignment caused the previously-active
        // session's agent to be updated instead of the one just selected,
        // leaving the UI and backend out of sync after every switch.
        this.currentSessionId = sessionId
        persistSessionId(sessionId)

        if (sessionAgentId && sessionAgentId !== this.agentId) {
          await this.setAgent(sessionAgentId)
        }

        if (session) {
          this.currentModelId = session.currentModelId ?? ''
          this.currentProviderId = session.currentProviderId ?? ''
        }

        this.messages = await fetchSessionMessages(sessionId)
      } finally {
        this.isLoading = false
      }
    },

    async loadSessionByAgentId(agentId: string): Promise<boolean> {
      const session = this.sessions.find(
        (s) => (s.currentAgentId ?? s.agentId) === agentId,
      )
      if (!session) return false

      await this.loadSessionMessages(session.id)
      return true
    },

    async sendMessage(content: string): Promise<void> {
      const text = content.trim()
      if (!text || this.isLoading) {
        return
      }

      this.error = null
      this.isLoading = true
      this.isStreaming = false

      const optimisticMessage: Message = {
        id: `temp-${Date.now()}`,
        role: 'user',
        content: text,
        timestamp: new Date().toISOString(),
      }
      this.messages.push(optimisticMessage)

      let eventSource: EventSource | null = null
      try {
        let sessionId = this.currentSessionId
        if (!sessionId) {
          const session = await createSession(this.agentId)
          sessionId = session.id
          this.currentSessionId = sessionId
          persistSessionId(sessionId)
        }

        eventSource = subscribeSessionStream(sessionId)
        eventSource.addEventListener('message', (event) => {
          this.applyContentEvent((event as MessageEvent).data as string)
        })

        await sendSessionMessage(sessionId, text)

        this.messages = await fetchSessionMessages(sessionId)
        const seen = new Set<string>()
        this.messages = this.messages.filter((message) => {
          if (seen.has(message.id)) return false
          seen.add(message.id)
          return true
        })
        await this.loadSessions()
      } catch (error) {
        this.error = error instanceof Error ? error.message : 'Failed to send message'
      } finally {
        if (eventSource) {
          eventSource.close()
        }
        this.isLoading = false
        this.isStreaming = false
      }
    },

    applyDelegationEvent(payload: string): void {
      let info: {
        chain_id?: string
        target_agent?: string
        tool_calls?: number
        last_tool?: string
        status?: string
      }
      try {
        info = JSON.parse(payload)
      } catch {
        return
      }

      const target =
        this.messages.find(
          (message) =>
            message.status !== 'completed' &&
            info.chain_id !== undefined &&
            message.chainId === info.chain_id,
        ) ??
        this.messages.find(
          (message) =>
            message.status !== 'completed' &&
            info.target_agent !== undefined &&
            message.targetAgent === info.target_agent,
        ) ??
        this.messages.find((message) => message.status !== 'completed' && message.role === 'assistant')

      if (!target) {
        return
      }

      if (info.target_agent !== undefined) {
        target.targetAgent = info.target_agent
      }
      if (info.chain_id !== undefined) {
        target.chainId = info.chain_id
      }
      if (info.tool_calls !== undefined) {
        target.toolCalls = info.tool_calls
      }
      if (info.last_tool !== undefined) {
        target.lastTool = info.last_tool
      }
      if (info.status !== undefined) {
        target.status = info.status
      }
    },

    applyContentEvent(payload: string): void {
      if (payload === '[DONE]') {
        this.isStreaming = false
        return
      }

      let info: Record<string, unknown>
      try {
        info = JSON.parse(payload)
      } catch {
        return
      }

      if (info.type === 'tool_call') {
        this.handleToolCallEvent(info)
        return
      }

      if (info.type === 'tool_result') {
        this.handleToolResultEvent(info)
        return
      }

      if (info.type === 'skill_load') {
        this.handleToolCallEvent({ ...info, name: info.name, status: 'running' })
        return
      }

      if (
        info.target_agent !== undefined ||
        info.chain_id !== undefined ||
        info.tool_calls !== undefined ||
        info.last_tool !== undefined
      ) {
        this.applyDelegationEvent(payload)
        return
      }

      if (info.content !== undefined) {
        this.handleContentChunk(info)
        return
      }

      if (info.error !== undefined) {
        this.error = String(info.error)
      }
    },

    handleContentChunk(info: { content?: unknown }): void {
      if (typeof info.content !== 'string' || info.content.length === 0) {
        return
      }

      let target = [...this.messages].reverse().find(
        (message) => message.role === 'assistant' && message.status !== 'completed',
      )

      if (!target) {
        target = {
          id: `streaming-${Date.now()}`,
          role: 'assistant',
          content: '',
          timestamp: new Date().toISOString(),
          status: 'running',
        }
        this.messages.push(target)
      }

      target.content = (target.content ?? '') + info.content
      target.status = 'running'
      this.isStreaming = true
    },

    handleToolCallEvent(info: { name?: unknown; status?: unknown; type?: unknown }): void {
      const toolName = String(info.name ?? info.type ?? 'unknown')
      const status = String(info.status ?? 'running')

      const toolMessage: Message = {
        id: `tool-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
        role: 'tool_result',
        toolName,
        content: '',
        timestamp: new Date().toISOString(),
        status,
      }

      this.messages.push(toolMessage)
    },

    handleToolResultEvent(info: { content?: unknown }): void {
      const target = [...this.messages].reverse().find(
        (message) => message.role === 'tool_result' && message.status === 'running',
      )

      if (!target) {
        return
      }

      target.content = String(info.content ?? '')
      target.status = 'completed'
    },
  },
})
