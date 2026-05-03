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

/**
 * Collapse adjacent tool_call/tool_result pairs into a single message.
 *
 * Mirrors the TUI behaviour (internal/tui/uikit/widgets/message.go) where
 * a tool_call is suppressed in favour of its paired tool_result, so the UI
 * renders one rich block per tool invocation instead of two. Pairing is by
 * adjacency and toolName equality; tool_error and unmatched tool_call rows
 * are preserved.
 */
export function collapseToolPairs(messages: Message[]): Message[] {
  const out: Message[] = []
  for (let i = 0; i < messages.length; i += 1) {
    const current = messages[i]
    if (current.role === 'tool_call') {
      const next = messages[i + 1]
      if (next && next.role === 'tool_result' && next.toolName === current.toolName) {
        continue
      }
    }
    out.push(current)
  }
  return out
}
