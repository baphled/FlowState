<script setup lang="ts">
// TodoTool.vue
//
// Renders a `todowrite` tool_result as a checkbox list rather than dumping
// the raw JSON. Mirrors the canonical TUI formatter at
// internal/tui/uikit/widgets/todo_widget.go::FormatTodoList — same status
// vocabulary (pending / in_progress / completed / cancelled), same fall-back
// strings ("Todo list cleared", "todos updated") so behaviour stays uniform
// across surfaces.
//
// The web frontend already uses markdown-it (commit 0a380ad) but markdown-it
// does not render `[ ]` / `[x]` as actual checkboxes by default and we want
// to avoid pulling in the markdown-it-task-lists plugin for one widget. The
// list is therefore rendered directly as styled list items here, with the
// checkbox character chosen by status. No new dependency required.
import { computed } from "vue";
import ToolBubble from "./ToolBubble.vue";
import type { ToolRendererProps } from "./toolRendererProps";

interface TodoEntry {
  content: string;
  status: "pending" | "in_progress" | "completed" | "cancelled";
  priority?: string;
}

interface RawTodoEntry {
  content?: unknown;
  status?: unknown;
  priority?: unknown;
}

const props = withDefaults(defineProps<ToolRendererProps>(), {
  status: "completed",
});

const known: ReadonlyArray<TodoEntry["status"]> = [
  "pending",
  "in_progress",
  "completed",
  "cancelled",
];

function normaliseStatus(raw: unknown): TodoEntry["status"] {
  if (typeof raw !== "string") return "pending";
  return (known as readonly string[]).includes(raw)
    ? (raw as TodoEntry["status"])
    : "pending";
}

function parseEntries(
  body: string,
): { ok: true; entries: TodoEntry[] } | { ok: false } {
  if (!body) return { ok: true, entries: [] };
  let parsed: unknown;
  try {
    parsed = JSON.parse(body);
  } catch {
    return { ok: false };
  }
  if (!Array.isArray(parsed)) return { ok: false };
  const entries: TodoEntry[] = [];
  for (const item of parsed as RawTodoEntry[]) {
    if (typeof item.content !== "string" || item.content.length === 0) continue;
    entries.push({
      content: item.content,
      status: normaliseStatus(item.status),
      priority: typeof item.priority === "string" ? item.priority : undefined,
    });
  }
  return { ok: true, entries };
}

const parsed = computed(() => parseEntries(props.body));

const fallbackMessage = computed(() => {
  if (parsed.value.ok && parsed.value.entries.length === 0) {
    return "Todo list cleared";
  }
  if (!parsed.value.ok) {
    return "todos updated";
  }
  return null;
});

const entries = computed<TodoEntry[]>(() =>
  parsed.value.ok ? parsed.value.entries : [],
);

const activeCount = computed(
  () =>
    entries.value.filter(
      (entry) => entry.status !== "completed" && entry.status !== "cancelled",
    ).length,
);

function checkboxFor(status: TodoEntry["status"]): string {
  switch (status) {
    case "completed":
      return "[x]";
    case "cancelled":
      return "[-]";
    case "in_progress":
      return "[~]";
    default:
      return "[ ]";
  }
}

const subtitle = computed(() => {
  if (!parsed.value.ok || entries.value.length === 0) return undefined;
  return `${activeCount.value} active / ${entries.value.length} total`;
});

// UI Parity I4 (May 2026): the todo widget is always tabular and the
// subtitle already shows N active / M total. Collapse-by-default keeps
// the thread compact; users can expand to see the full checkbox list.
// Todos do not have an error status path, but mirror the heuristic for
// consistency with the rest of the tool surface.
const cardDefaultOpen = computed(() => props.status === "error");
</script>

<template>
  <ToolBubble
    :tool-name="props.toolName"
    title="Todos"
    :subtitle="subtitle"
    :status="props.status"
    :default-open="cardDefaultOpen"
  >
    <div class="tool-renderer" data-component="todo-tool">
      <p v-if="fallbackMessage" class="todo-fallback">{{ fallbackMessage }}</p>
      <ul v-else class="todo-list">
        <li
          v-for="(entry, index) in entries"
          :key="`${index}-${entry.content}`"
          class="todo-item"
          :class="`todo-item--${entry.status}`"
          data-testid="todo-item"
          :data-status="entry.status"
        >
          <span class="todo-box" aria-hidden="true">{{
            checkboxFor(entry.status)
          }}</span>
          <span class="todo-content">{{ entry.content }}</span>
        </li>
      </ul>
    </div>
  </ToolBubble>
</template>

<style scoped>
.tool-renderer {
  display: grid;
  gap: 0.45rem;
}

.todo-list {
  list-style: none;
  padding: 0;
  margin: 0;
  display: grid;
  gap: 0.25rem;
}

.todo-item {
  display: flex;
  align-items: baseline;
  gap: 0.6rem;
  padding: 0.2rem 0.4rem;
  border-radius: calc(var(--radius, 12px) - 6px);
  font-family:
    ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono",
    "Courier New", monospace;
  font-size: 0.85rem;
  line-height: 1.4;
}

.todo-box {
  color: var(--text-muted, #565f89);
  font-weight: 600;
  min-width: 1.7rem;
}

.todo-item--completed .todo-box {
  color: #9ece6a;
}

.todo-item--in_progress .todo-box {
  color: var(--accent, #7aa2f7);
}

.todo-item--cancelled .todo-box {
  color: var(--error, #f7768e);
}

.todo-content {
  color: var(--text-primary, #c0caf5);
  flex: 1;
}

.todo-item--completed .todo-content,
.todo-item--cancelled .todo-content {
  color: var(--text-muted, #565f89);
  text-decoration: line-through;
}

.todo-fallback {
  margin: 0;
  color: var(--text-muted, #565f89);
  font-style: italic;
  font-size: 0.85rem;
}
</style>
