import { describe, it, expect, vi, beforeEach } from "vitest";
import { mount } from "@vue/test-utils";
import { setActivePinia, createPinia } from "pinia";
import AgentSwitcher from "./AgentSwitcher.vue";

vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
  };
});

// UI Parity PR6 I1 residual (May 2026) — AgentSwitcher's trigger button
// previously rendered a raw 🤖 emoji glyph. The Icon wrapper introduced in
// PR2 normalises every chrome glyph onto Lucide, so the bot emoji here is
// the last residual on the agent-switcher surface. The component must
// render <Icon name="bot" /> instead of a literal emoji.
describe("AgentSwitcher (I1 residual — emoji → Icon)", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  it('renders <Icon name="bot" /> in the trigger, not a 🤖 emoji', () => {
    const wrapper = mount(AgentSwitcher);
    const trigger = wrapper.get('[data-testid="agent-switcher"]');

    // The Icon wrapper stamps data-icon-name on its rendered SVG.
    const botIcon = trigger.find('[data-testid="icon"][data-icon-name="bot"]');
    expect(botIcon.exists()).toBe(true);

    // The 🤖 emoji must NOT be present in the trigger button text.
    expect(trigger.text()).not.toContain("🤖");
  });
});
