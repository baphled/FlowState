import { isContextTool } from "@/tools/toolRegistry";
import type { Agent, Message } from "@/types";

export interface GroupedMessage {
  type: "message";
  message: Message;
}

export interface ContextGroup {
  type: "context-group";
  messages: Message[];
  toolCounts: Record<string, number>;
}

export type GroupedMessageEntry = GroupedMessage | ContextGroup;

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
  if (message.role !== "assistant") {
    return undefined;
  }

  const candidateId = message.agentId ?? activeAgentId;
  if (!candidateId) {
    return undefined;
  }

  return agents.find((agent) => agent.id === candidateId)?.name;
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
  const out: Message[] = [];
  for (let i = 0; i < messages.length; i += 1) {
    const current = messages[i];
    if (current.role === "tool_call") {
      const next = messages[i + 1];
      if (
        next &&
        next.role === "tool_result" &&
        next.toolName === current.toolName
      ) {
        continue;
      }
    }
    out.push(current);
  }
  return out;
}

/**
 * UI Parity bug-fix bundle (May 2026). P2-9: precompute "preceding user
 * message" lookups once per messages array so MessageBubble no longer
 * re-scans the entire array per chunk. Walks the messages list in a
 * single pass tracking the most-recent user message; for each assistant
 * message encountered, stores a tuple keyed by assistant id. Returns a
 * Map so callers get O(1) lookups in the render loop.
 *
 * Only `assistant` messages are keyed; tool / thinking / system roles
 * are skipped because they have no "regenerate" affordance. Returns
 * `null` when there is no preceding user message — the bubble hides the
 * Regenerate button in that case.
 */
export function buildPrecedingUserPromptMap(
  messages: Message[],
): Map<string, { id: string; content: string } | null> {
  const map = new Map<string, { id: string; content: string } | null>();
  let lastUser: { id: string; content: string } | null = null;
  for (const m of messages) {
    if (m.role === "user") {
      lastUser = { id: m.id, content: m.content };
      continue;
    }
    if (m.role === "assistant") {
      map.set(m.id, lastUser);
    }
  }
  return map;
}

/**
 * Group consecutive context tools (tool_result role and isContextTool is true)
 * into a single group entry. Single context tools are not grouped.
 */
export function groupContextTools(messages: Message[]): GroupedMessageEntry[] {
  const result: GroupedMessageEntry[] = [];
  let i = 0;

  while (i < messages.length) {
    const current = messages[i];
    if (
      current.role === "tool_result" &&
      current.toolName &&
      isContextTool(current.toolName)
    ) {
      const group: Message[] = [current];
      let j = i + 1;
      while (j < messages.length) {
        const next = messages[j];
        if (
          next.role === "tool_result" &&
          next.toolName &&
          isContextTool(next.toolName)
        ) {
          group.push(next);
          j++;
        } else {
          break;
        }
      }

      if (group.length >= 2) {
        const toolCounts: Record<string, number> = {};
        for (const msg of group) {
          const name = msg.toolName!;
          toolCounts[name] = (toolCounts[name] || 0) + 1;
        }
        result.push({
          type: "context-group",
          messages: group,
          toolCounts,
        });
        i = j;
        continue;
      }
    }

    result.push({ type: "message", message: current });
    i++;
  }

  return result;
}
