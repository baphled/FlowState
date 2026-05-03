import type { Agent, Message } from '@/types'

/**
 * Resolve the human-readable display name for the agent that produced a
 * message. Assistant messages without an explicit agentId fall back to the
 * currently active agent so streaming responses surface the right name
 * before the backend has stamped one. Returns undefined when no match is
 * found, so the bubble can fall back to the role label.
 */
export function resolveAgentName(
  message: Message,
  agents: Agent[],
  activeAgentId: string,
): string | undefined {
  if (message.role !== 'assistant') {
    return undefined
  }

  const candidateId = message.agentId ?? activeAgentId
  if (!candidateId) {
    return undefined
  }

  return agents.find((agent) => agent.id === candidateId)?.name
}
