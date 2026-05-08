/** Domain types mirroring the FlowState Go structs. */

/**
 * ThinkingBlock mirrors `provider.ThinkingBlock` on the Go side. Carried on
 * an assistant message so that a session reload can reconstruct the exact
 * thinking blocks the backend persisted (signed or redacted variants).
 *
 * The chat UI does not visibly render the contents — these are private
 * model reasoning. They are surfaced here for two reasons:
 *
 *   1. The presence of a non-empty `thinkingBlocks` array on an
 *      otherwise-empty assistant message is the signature of the
 *      backend accumulator's "thinking-only degraded turn" placeholder
 *      (see `Empty-Content Thinking-Only Assistant Turn (May 2026)` in
 *      the FlowState vault). The chat UI uses that combination —
 *      `content === ""` + `thinkingBlocks.length > 0` + non-empty
 *      `stopReason` — to render a soft-error affordance instead of a
 *      blank bubble that would look like a stall.
 *   2. A future on-demand "show reasoning" disclosure can read these
 *      directly without a separate fetch.
 */
export interface ThinkingBlock {
  thinking?: string
  signature?: string
  redacted?: boolean
  data?: string
}

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
  /**
   * providerName carries the provider that produced this message
   * (e.g. "anthropic", "zai", "openai"), stamped by the engine on
   * the StreamChunk and persisted by the session accumulator at flush
   * time. Paired with modelName so per-turn attribution survives
   * reload and a mid-stream failover that switches providers is
   * reflected on the message itself, not just the session-level
   * currentProviderId.
   *
   * Currently consumed by the activity-indicator chip when it falls
   * back to per-message data (e.g. when restoring a session whose
   * top-level currentProviderId has not yet been promoted from the
   * latest assistant message). A future per-bubble badge will read
   * this directly to show "produced by glm-4.6 · zai".
   */
  providerName?: string
  /**
   * Model-reasoning text accumulated from `type: "thinking"` SSE events
   * (Drop #2 in the Streaming Signal-Drop fix). Carries the provider's
   * private step-by-step reasoning (Anthropic thinking_delta, glm-4.6
   * reasoning_content). MUST NOT be rendered as the visible reply — the
   * UI affordance to disclose this on demand is Track B's work. Until
   * that ships the field is plumbed end-to-end so the watchdog re-arms
   * during the reasoning phase and the data is captured for later display.
   */
  thinkingContent?: string
  /**
   * thinkingBlocks carries the structured per-block thinking content
   * the backend accumulator persisted on this assistant message
   * (Anthropic extended-thinking signed/redacted blocks, or the
   * synthetic blocks the May 2026 thinking-only-degraded-turn fix
   * attaches when a reasoning provider produces no visible content).
   * Mirrors `session.Message.ThinkingBlocks` on the Go side.
   *
   * Read by the MessageBubble degraded-turn affordance to distinguish
   * the placeholder shape from a true stall — see
   * `Empty-Content Thinking-Only Assistant Turn (May 2026)` in the
   * FlowState vault.
   */
  thinkingBlocks?: ThinkingBlock[]
  /**
   * stopReason is the upstream provider's terminal stop reason for the
   * turn that produced this message. Empty when unknown. Mirrors
   * `session.Message.StopReason` on the Go side.
   *
   * Combined with `content === ""` and a non-empty `thinkingBlocks`
   * array, identifies the synthesised assistant placeholder for a
   * thinking-only degraded turn.
   */
  stopReason?: string
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
 * Swarm mirrors the shape returned by GET /api/swarms — a compact
 * projection of swarm.Manifest carrying just the fields the web
 * @-picker and any future web swarm panel needs. The full manifest
 * (gates, harness, retry, circuit breaker) stays server-side so the
 * web client never has to reason about gate kinds or precedence.
 *
 * Field-by-field correspondence to internal/swarm/manifest.go:
 *   id          ← Manifest.ID
 *   description ← Manifest.Description (may be empty)
 *   lead        ← Manifest.Lead (the agent or sub-swarm id that runs first)
 *   members     ← Manifest.Members (always an array; never null)
 */
export interface Swarm {
  id: string
  description?: string
  lead: string
  members: string[]
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
  /**
   * Phase 3 — TUI-cadence parity. Carries the engine's current
   * context_usage shape on agent / model PATCH responses so the
   * chat UI's chip ticks up to reflect the new (provider, model,
   * messages) state without waiting for the next pre-send streamed
   * event. Wire shape mirrors the streamed `context_usage` SSE
   * event payload exactly. Omitted when the engine cannot compute a
   * meaningful figure (no token counter, no resolvable limit).
   */
  contextUsage?: {
    input_tokens: number
    output_reserve: number
    limit: number
    percentage: number
    provider: string
    model: string
  }
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
