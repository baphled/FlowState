import { describe, it, expect } from "vitest";
import { mount } from "@vue/test-utils";
import GrepTool from "./GrepTool.vue";

const CopyButton = {
  template: '<span data-testid="copy-btn" />',
};

const ToolBubble = {
  props: ["toolName", "title", "subtitle", "status", "defaultOpen"],
  template: `
    <div data-testid="tool-bubble" data-component="tool" :data-tool="toolName" :data-status="status" :data-default-open="defaultOpen ? 'true' : 'false'">
      <span data-testid="tool-title">{{ title }}</span>
      <span v-if="subtitle" data-testid="tool-subtitle">{{ subtitle }}</span>
      <slot />
    </div>
  `,
};

describe("GrepTool", () => {
  // I4: Grep tool output is a long match list. Start collapsed; the
  // subtitle still surfaces the pattern so the user knows what was searched.
  it("starts collapsed by default (long-match-list category)", () => {
    const wrapper = mount(GrepTool, {
      props: {
        toolName: "grep",
        heading: "TODO",
        body: "a:1\nb:1",
        status: "completed",
      },
      global: { stubs: { CopyButton, ToolBubble } },
    });
    expect(
      wrapper
        .get('[data-testid="tool-bubble"]')
        .attributes("data-default-open"),
    ).toBe("false");
  });

  it("forces open when status is error", () => {
    const wrapper = mount(GrepTool, {
      props: {
        toolName: "grep",
        heading: "TODO",
        body: "syntax error",
        status: "error",
      },
      global: { stubs: { CopyButton, ToolBubble } },
    });
    expect(
      wrapper
        .get('[data-testid="tool-bubble"]')
        .attributes("data-default-open"),
    ).toBe("true");
  });
  it("renders grep results and bubble metadata", () => {
    const wrapper = mount(GrepTool, {
      props: {
        toolName: "grep",
        heading: "TODO",
        body: "src/a.ts:1:TODO found",
        status: "completed",
      },
      global: {
        stubs: {
          CopyButton,
          ToolBubble,
        },
      },
    });

    expect(
      wrapper.get('[data-testid="tool-bubble"]').attributes("data-component"),
    ).toBe("tool");
    expect(
      wrapper.get('[data-testid="tool-bubble"]').attributes("data-tool"),
    ).toBe("grep");
    expect(wrapper.get('[data-testid="tool-subtitle"]').text()).toBe("TODO");
    expect(wrapper.get('[data-component="grep-tool"]').text()).toContain(
      "src/a.ts:1:TODO found",
    );
    expect(wrapper.find('[data-testid="copy-btn"]').exists()).toBe(true);
  });
});
