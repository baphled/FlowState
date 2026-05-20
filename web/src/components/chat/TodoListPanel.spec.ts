import { describe, expect, it, beforeEach, vi } from "vitest";
import { mount, flushPromises } from "@vue/test-utils";
import { setActivePinia, createPinia } from "pinia";
import TodoListPanel from "./TodoListPanel.vue";
import { useTodoStore } from "@/stores/todoStore";
import type { Todo } from "@/stores/todoStore";

// Some sibling specs (e.g. ChatView.spec.ts) install partial localStorage
// mocks that lack `.clear()`. Provide a minimal in-memory shim local to
// this suite so the todo store starts each test with a clean slate without
// depending on the order of file execution.
const memoryStorage: Record<string, string> = {};
const storageMock: Storage = {
  get length() {
    return Object.keys(memoryStorage).length;
  },
  key: vi.fn((idx: number) => Object.keys(memoryStorage)[idx] ?? null),
  getItem: vi.fn((key: string) => memoryStorage[key] ?? null),
  setItem: vi.fn((key: string, value: string) => {
    memoryStorage[key] = value;
  }),
  removeItem: vi.fn((key: string) => {
    delete memoryStorage[key];
  }),
  clear: vi.fn(() => {
    for (const key of Object.keys(memoryStorage)) {
      delete memoryStorage[key];
    }
  }),
};

Object.defineProperty(window, "localStorage", {
  value: storageMock,
  configurable: true,
  writable: true,
});

// Seed the store directly — todos are agent-emitted, the store has no
// user-add action. Tests stand in for the agent emission pipeline by
// driving the same actions chatStore would (setCurrentSession +
// ingestToolResult). We never reach for $patch on the bySession map so the
// session-keyed contract stays exercised by every spec.
function seedTodos(todos: Todo[], sessionId = "session-test"): void {
  const store = useTodoStore();
  store.setCurrentSession(sessionId);
  // Reuse the public ingestion seam — going through ingestToolResult means
  // these tests pin the same path the real SSE pipeline drives, which is
  // exactly what the panel renders in production.
  const payload = JSON.stringify(
    todos.map((t) => ({
      content: t.content,
      status: t.status === "completed" ? "completed" : "pending",
      priority: "low",
    })),
  );
  store.ingestToolResult(sessionId, payload);
}

function makeTodo(overrides: Partial<Todo> = {}): Todo {
  return {
    id: overrides.id ?? `todo-${Math.random().toString(36).slice(2, 8)}`,
    content: overrides.content ?? "sample todo",
    status: overrides.status ?? "pending",
    createdAt: overrides.createdAt ?? "2026-05-04T09:00:00Z",
    completedAt: overrides.completedAt,
  };
}

describe("TodoListPanel", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    storageMock.clear();
  });

  it("renders the live todo list from the shared todoStore", async () => {
    seedTodos([
      makeTodo({ content: "write side-panel spec" }),
      makeTodo({ content: "extract TodoListPanel" }),
    ]);

    const wrapper = mount(TodoListPanel);
    await flushPromises();

    const items = wrapper.findAll('[data-testid="todo-item"]');
    expect(items).toHaveLength(2);
    expect(wrapper.text()).toContain("write side-panel spec");
    expect(wrapper.text()).toContain("extract TodoListPanel");
  });

  it("reflects subsequent updates to the shared store without remounting", async () => {
    const wrapper = mount(TodoListPanel);
    await flushPromises();

    expect(wrapper.findAll('[data-testid="todo-item"]')).toHaveLength(0);

    const store = useTodoStore();
    store.setCurrentSession("session-test");
    store.ingestToolResult(
      "session-test",
      JSON.stringify([
        { content: "emitted after mount", status: "pending", priority: "low" },
      ]),
    );
    await flushPromises();

    const items = wrapper.findAll('[data-testid="todo-item"]');
    expect(items).toHaveLength(1);
    expect(wrapper.text()).toContain("emitted after mount");
  });

  it("swaps the rendered list when the active session changes", async () => {
    const store = useTodoStore();
    store.setCurrentSession("session-A");
    store.ingestToolResult(
      "session-A",
      JSON.stringify([
        { content: "todo for A", status: "pending", priority: "low" },
      ]),
    );
    store.ingestToolResult(
      "session-B",
      JSON.stringify([
        { content: "todo for B", status: "pending", priority: "low" },
        { content: "second for B", status: "pending", priority: "low" },
      ]),
    );

    const wrapper = mount(TodoListPanel);
    await flushPromises();
    expect(wrapper.text()).toContain("todo for A");
    expect(wrapper.text()).not.toContain("todo for B");

    store.setCurrentSession("session-B");
    await flushPromises();

    const items = wrapper.findAll('[data-testid="todo-item"]');
    expect(items).toHaveLength(2);
    expect(wrapper.text()).toContain("todo for B");
    expect(wrapper.text()).not.toContain("todo for A");
  });

  it("renders the session-aware empty-state copy when the current session has no todos", async () => {
    const store = useTodoStore();
    store.setCurrentSession("session-fresh");

    const wrapper = mount(TodoListPanel);
    await flushPromises();

    const empty = wrapper.find('[data-testid="todo-empty"]');
    expect(empty.exists()).toBe(true);
    // The previous global "No tasks yet" copy implied user-add semantics; the
    // session-scoped panel should make the scope explicit.
    expect(empty.text()).toContain("No todos in this session yet");
  });

  it("exposes a stable testid hook for the side-panel mount point", () => {
    const wrapper = mount(TodoListPanel);
    expect(wrapper.find('[data-testid="todo-list-panel"]').exists()).toBe(true);
  });

  it("does not surface a user-add affordance in the template", () => {
    const wrapper = mount(TodoListPanel);
    // The user is a pure observer — todos are agent-emitted via the
    // todowrite tool. No add input, no add button, no add hook.
    expect(wrapper.find('[data-testid="todo-input"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="todo-add-btn"]').exists()).toBe(false);
    expect(wrapper.find('input[type="text"]').exists()).toBe(false);
  });

  it("does not surface a per-item delete or toggle affordance", async () => {
    seedTodos([makeTodo({ content: "agent emitted" })]);

    const wrapper = mount(TodoListPanel);
    await flushPromises();

    expect(wrapper.find('[data-testid="todo-delete-btn"]').exists()).toBe(
      false,
    );
    // Status indicator may exist (e.g. a glyph), but no interactive checkbox
    // or click handler that would mutate store state.
    expect(
      wrapper.find('[data-testid="todo-item"] input[type="checkbox"]').exists(),
    ).toBe(false);
  });
});
