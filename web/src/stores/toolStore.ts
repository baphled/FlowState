import { computed } from "vue";
import { defineStore } from "pinia";
import { useSwarmStore } from "@/stores/swarmStore";

export interface ToolCallEvent {
  id: string;
  toolName: string;
  status: "pending" | "running" | "completed" | "error";
  startedAt: string;
  completedAt?: string;
  arguments?: Record<string, unknown>;
  result?: string;
  agentId: string;
}

export const useToolStore = defineStore("tool", () => {
  const swarmStore = useSwarmStore();

  const toolEvents = computed<ToolCallEvent[]>(() => {
    return swarmStore.events
      .filter((e) => e.type === "tool_call")
      .map((event): ToolCallEvent => {
        const meta = event.metadata || {};
        const statusMap: Record<string, ToolCallEvent["status"]> = {
          started: "running",
          pending: "pending",
          completed: "completed",
          error: "error",
        };
        return {
          id: event.id,
          toolName: String(meta.tool_name || meta.tool || "unknown"),
          status: statusMap[event.status || "running"] || "pending",
          startedAt: event.timestamp,
          arguments: meta.arguments as Record<string, unknown> | undefined,
          result: meta.result as string | undefined,
          agentId: event.agent_id,
        };
      })
      .sort(
        (a, b) =>
          new Date(b.startedAt).getTime() - new Date(a.startedAt).getTime(),
      );
  });

  const toolCount = computed(() => toolEvents.value.length);
  const runningCount = computed(
    () => toolEvents.value.filter((t) => t.status === "running").length,
  );

  return {
    toolEvents,
    toolCount,
    runningCount,
  };
});
