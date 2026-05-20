// MessageBubble.cardChrome.spec.ts
//
// One tool invocation = one card. The chrome-nesting bug rendered every
// tool_result with TWO ToolBubble layers — one in MessageBubble.vue around
// the per-tool component, and one inside each per-tool component. This spec
// pins the contract: when MessageBubble renders a tool_result it must produce
// exactly one tool-bubble in the resulting DOM, regardless of which per-tool
// component handles the rendering.
import { describe, expect, it, beforeEach, vi } from "vitest";
import { mount } from "@vue/test-utils";
import { createRouter, createMemoryHistory } from "vue-router";
import { defineComponent, h } from "vue";
import { createPinia, setActivePinia } from "pinia";
import MessageBubble from "./MessageBubble.vue";
import type { Message } from "@/types";
import { useChatStore } from "@/stores/chatStore";
import { registerTools } from "@/tools/registerTools";

vi.mock("@/stores/chatStore", () => ({
  useChatStore: vi.fn(),
}));

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: "/",
        name: "home",
        component: defineComponent({ render: () => h("div") }),
      },
    ],
  });
}

function makeMessage(overrides: Partial<Message> = {}): Message {
  return {
    id: "msg-1",
    role: "tool_result",
    content: "",
    timestamp: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function mountWithRealTools(message: Message) {
  return mount(MessageBubble, {
    props: { message },
    global: {
      plugins: [makeRouter()],
    },
  });
}

describe("MessageBubble — single card chrome per tool call", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    registerTools();
    vi.mocked(useChatStore).mockReturnValue({
      loadSessionByAgentId: vi.fn(),
      loadSessionForDelegation: vi.fn(),
    } as never);
  });

  const cases: Array<{ tool: string; toolInput?: string; body: string }> = [
    {
      tool: "bash",
      toolInput: JSON.stringify({ command: "ls" }),
      body: "a\nb",
    },
    {
      tool: "read",
      toolInput: JSON.stringify({ filePath: "foo.txt" }),
      body: "contents",
    },
    {
      tool: "write",
      toolInput: JSON.stringify({ filePath: "bar.txt" }),
      body: "wrote it",
    },
    {
      tool: "edit",
      toolInput: JSON.stringify({ filePath: "baz.txt" }),
      body: "-old\n+new",
    },
    {
      tool: "grep",
      toolInput: JSON.stringify({ pattern: "foo" }),
      body: "match-1",
    },
    {
      tool: "glob",
      toolInput: JSON.stringify({ pattern: "*.ts" }),
      body: "a.ts\nb.ts",
    },
    { tool: "task", body: "output" },
    {
      tool: "todowrite",
      body: JSON.stringify([
        { content: "one", status: "pending", priority: "medium" },
        { content: "two", status: "completed", priority: "low" },
      ]),
    },
    {
      tool: "search_context",
      toolInput: JSON.stringify({ query: "find me" }),
      body: "user: hit one\n---\nassistant: hit two",
    },
  ];

  for (const { tool, toolInput, body } of cases) {
    it(`renders exactly one card layer for a ${tool} tool_result`, () => {
      const wrapper = mountWithRealTools(
        makeMessage({ toolName: tool, toolInput, content: body }),
      );

      // Count every concrete card chrome layer the renderer puts around the
      // tool body. ToolBubble is the canonical chrome (`.tool-bubble` from
      // the scoped styles). Two layers is the bug; one is the contract.
      const bubbles = wrapper.findAll(".tool-bubble");
      expect(bubbles).toHaveLength(1);
    });
  }
});
