/** Domain types mirroring the FlowState Go structs. */

export interface Message {
  role: 'user' | 'assistant' | 'system'
  content: string
}

export interface ChatRequest {
  messages: Message[]
  model: string
}

export interface ChatResponse {
  content: string
}

export type SwarmEventType = 'delegation' | 'tool_call' | 'plan' | 'review'

export interface SwarmEvent {
  id: string
  type: SwarmEventType
  agentName: string
  payload: Record<string, unknown>
  timestamp: string
}

export interface HealthResponse {
  status: 'ok' | 'degraded'
}

export type Theme = 'dark' | 'light' | 'terminal'
