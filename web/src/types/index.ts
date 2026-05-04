/** Domain types mirroring the FlowState Go structs. */

export interface Message {
  id: string
  role: string
  content: string
  agentId?: string
  toolName?: string
  toolInput?: string
  timestamp: string
  targetAgent?: string
  chainId?: string
  toolCalls?: number
  lastTool?: string
  status?: string
  modelName?: string
}

export interface ChatRequest {
  agent_id: string
  message: string
}

export interface ChatResponse {
  content: string
}

export interface SSEChunk {
  content?: string
  error?: string
}

export interface SwarmEvent {
  id: string
  type: string
  status?: string
  timestamp: string
  agent_id: string
  metadata?: Record<string, unknown>
  schema_version?: number
}

export interface HealthResponse {
  status: 'ok' | 'degraded'
}

export type Theme = 'dark' | 'light' | 'terminal'

export interface Agent {
  id: string
  name: string
  description?: string
  version?: string
  instructions?: string
  model?: string
  provider?: string
  capabilities?: {
    skills?: string[]
    tools?: string[]
  }
}

export interface Session {
  id: string
  agentId: string
  currentAgentId?: string
  currentModelId?: string
  currentProviderId?: string
  messages: Message[]
  messageCount: number
  createdAt: string
  updatedAt: string
}

export interface SessionSummary {
  id: string
  agentId: string
  currentAgentId?: string
  currentModelId?: string
  currentProviderId?: string
  parentId?: string
  title: string
  createdAt: string
  updatedAt: string
  messageCount: number
}

export interface ModelInfo {
  id: string
  name: string
}

export interface ProviderInfo {
  id: string
  models: ModelInfo[]
}

export interface ModelsResponse {
  providers: ProviderInfo[]
}

export interface Model {
  id: string
  name: string
  providerId: string
}

export interface SessionMessageRequest {
  content: string
}

export interface SessionMessageResponse {
  id: string
  role: string
  content: string
  createdAt: string
}
