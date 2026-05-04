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

export interface ModelPreference {
  provider: string
  model: string
}

export type ModelPolicy = 'permissive' | 'strict' | ''

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
  /**
   * Provider/model pairs the agent is intended to run on. Order is
   * significant — earlier entries are surfaced first by the picker.
   * Mirrors agent.Manifest.PreferredModels on the Go side.
   */
  preferred_models?: ModelPreference[]
  /**
   * Controls how preferred_models is interpreted:
   * - "" or "permissive": every model is allowed; preferred entries
   *   are ranked first and badged.
   * - "strict": only preferred entries are selectable; an empty list
   *   degrades to permissive (avoids locking the user out).
   */
  model_policy?: ModelPolicy
}

/**
 * Session and SessionSummary mirror SessionResponse in
 * internal/api/session_response.go. Hand-aligned (no codegen). The contract
 * spec in `types/contract.spec.ts` asserts every Go field is mirrored here so
 * a backend-only addition is caught at test time rather than runtime.
 *
 * Naming/required-ness rules from the Go side:
 *   - Camel-cased JSON tags (matches the SessionResponse contract).
 *   - Fields tagged `omitempty` on the Go side are TS optional.
 *   - Fields without `omitempty` (always emitted) are TS required.
 *
 * Specifically: `IsStreaming bool` has no omitempty — the wire ALWAYS
 * carries this field (even as `false`), so it is required here. Code that
 * defends against missing values can drop the `=== true` guard, but reading
 * the boolean defensively is still cheap and harmless.
 */
export interface Session {
  id: string
  agentId: string
  currentAgentId?: string
  currentModelId?: string
  currentProviderId?: string
  /** Lifecycle status emitted on every response (no omitempty in Go). */
  status: string
  /** Direct parent id (delegated child → parent). Optional — root sessions omit. */
  parentId?: string
  /** Session-level parent id, distinct from parentId in nested swarms. Optional. */
  parentSessionId?: string
  /** Depth in the delegation tree (0 = root). Always emitted. */
  depth: number
  messages: Message[]
  messageCount: number
  /** True when the backend broker has an active Publish in progress. Always emitted. */
  isStreaming: boolean
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
  /** Optional second parent id used by some swarm layouts. */
  parentSessionId?: string
  /** Lifecycle status emitted on every response. */
  status: string
  /** Depth in the delegation tree (0 = root). */
  depth: number
  title: string
  createdAt: string
  updatedAt: string
  messageCount: number
  /** True when the backend broker has an active Publish in progress for this session. */
  isStreaming: boolean
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
