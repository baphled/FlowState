import { describe, it, expect } from "vitest";
import { mount } from "@vue/test-utils";
import RecallSearchTool from "./RecallSearchTool.vue";

const ToolBubble = {
  props: ["toolName", "title", "subtitle", "status", "defaultOpen"],
  template: `
    <div data-testid="tool-bubble" data-component="tool" :data-tool="toolName" :data-status="status" :data-default-open="defaultOpen ? 'true' : 'false'">
      <slot />
    </div>
  `,
};

describe("RecallSearchTool", () => {
  // I4: Recall search results are long. Start collapsed; the subtitle
  // already surfaces the result count so the user can decide to open.
  it("starts collapsed by default (search-results category)", () => {
    const wrapper = mount(RecallSearchTool, {
      props: {
        toolName: "search_context",
        heading: "q",
        body: "",
        status: "completed",
      },
      global: { stubs: { ToolBubble } },
    });
    expect(
      wrapper
        .get('[data-testid="tool-bubble"]')
        .attributes("data-default-open"),
    ).toBe("false");
  });

  it("forces open when status is error", () => {
    const wrapper = mount(RecallSearchTool, {
      props: {
        toolName: "search_context",
        heading: "q",
        body: "",
        status: "error",
      },
      global: { stubs: { ToolBubble } },
    });
    expect(
      wrapper
        .get('[data-testid="tool-bubble"]')
        .attributes("data-default-open"),
    ).toBe("true");
  });
  it("renders the query and parsed result entries", () => {
    const body = [
      "user: how do I fix the bubble nesting?",
      "assistant: remove the outer wrapper",
      "user: thanks",
    ].join("\n---\n");

    const wrapper = mount(RecallSearchTool, {
      props: {
        toolName: "search_context",
        heading: "how do I fix the bubble nesting?",
        body,
        status: "completed",
        toolInput: JSON.stringify({
          query: "how do I fix the bubble nesting?",
        }),
      },
      global: { stubs: { ToolBubble } },
    });

    expect(wrapper.find('[data-component="recall-search-tool"]').exists()).toBe(
      true,
    );
    const query = wrapper.find('[data-testid="recall-query"]');
    expect(query.exists()).toBe(true);
    expect(query.text()).toContain("how do I fix the bubble nesting?");

    const results = wrapper.findAll('[data-testid="recall-result"]');
    expect(results).toHaveLength(3);
    expect(results[0].text()).toContain("user");
    expect(results[0].text()).toContain("how do I fix the bubble nesting?");
    expect(results[1].text()).toContain("assistant");
    expect(results[1].text()).toContain("remove the outer wrapper");
  });

  it("limits visible results to a sensible cap and shows an overflow hint", () => {
    const body = Array.from(
      { length: 12 },
      (_, i) => `user: result ${i + 1}`,
    ).join("\n---\n");

    const wrapper = mount(RecallSearchTool, {
      props: {
        toolName: "search_context",
        heading: "big query",
        body,
        status: "completed",
      },
      global: { stubs: { ToolBubble } },
    });

    const results = wrapper.findAll('[data-testid="recall-result"]');
    expect(results.length).toBeLessThanOrEqual(10);
    expect(results.length).toBeGreaterThanOrEqual(5);

    const overflow = wrapper.find('[data-testid="recall-overflow"]');
    expect(overflow.exists()).toBe(true);
    expect(overflow.text()).toMatch(/and \d+ more/);
  });

  it("renders a no-results state when the body is empty", () => {
    const wrapper = mount(RecallSearchTool, {
      props: {
        toolName: "search_context",
        heading: "no hits",
        body: "",
        status: "completed",
      },
      global: { stubs: { ToolBubble } },
    });

    expect(wrapper.find('[data-component="recall-search-tool"]').exists()).toBe(
      true,
    );
    expect(wrapper.find('[data-testid="recall-empty"]').exists()).toBe(true);
    expect(wrapper.findAll('[data-testid="recall-result"]')).toHaveLength(0);
  });

  // UI Parity PR6 N6 (May 2026) — recall results expose timestamp + chain
  // depth when carried in the formatted body. The current backend body shape
  // is `role: content`, but recall chain-search results can prefix each
  // chunk with `[time=<iso>] [depth=N]` markers when carrying provenance.
  // The parser must surface them as relative time ("2h ago") and an `↑N`
  // hop hint without breaking the plain `role: content` form. The
  // optional-prefix shape keeps the renderer forward-compatible with a
  // backend that has not yet been migrated.
  describe("N6 — timestamp + chain-hop hint", () => {
    it("renders a relative timestamp pulled from a [time=<iso>] prefix", () => {
      // Two hours ago, stable for the test by stubbing Date.now.
      const fixedNow = new Date("2026-05-12T12:00:00Z").getTime();
      const origNow = Date.now;
      Date.now = () => fixedNow;
      try {
        const twoHoursAgo = "2026-05-12T10:00:00Z";
        const body = `[time=${twoHoursAgo}] user: hello`;
        const wrapper = mount(RecallSearchTool, {
          props: {
            toolName: "search_context",
            heading: "q",
            body,
            status: "completed",
          },
          global: { stubs: { ToolBubble } },
        });
        const ts = wrapper.find('[data-testid="recall-timestamp"]');
        expect(ts.exists()).toBe(true);
        expect(ts.text()).toContain("2h ago");
        // Source label survives the prefix strip.
        expect(wrapper.find('[data-testid="recall-result"]').text()).toContain(
          "user",
        );
        // The raw prefix must NOT leak into the snippet.
        expect(
          wrapper.find('[data-testid="recall-result"]').text(),
        ).not.toContain("time=");
      } finally {
        Date.now = origNow;
      }
    });

    it("renders a chain-hop hint (↑N) from a [depth=N] prefix", () => {
      const body = "[depth=3] user: through three hops";
      const wrapper = mount(RecallSearchTool, {
        props: {
          toolName: "chain_search_context",
          heading: "q",
          body,
          status: "completed",
        },
        global: { stubs: { ToolBubble } },
      });
      const hop = wrapper.find('[data-testid="recall-chain-depth"]');
      expect(hop.exists()).toBe(true);
      expect(hop.text()).toBe("↑3");
      // The raw prefix must not leak.
      expect(
        wrapper.find('[data-testid="recall-result"]').text(),
      ).not.toContain("depth=");
    });

    it("renders both prefixes when present and preserves snippet content", () => {
      const fixedNow = new Date("2026-05-12T12:00:00Z").getTime();
      const origNow = Date.now;
      Date.now = () => fixedNow;
      try {
        const body =
          "[time=2026-05-12T11:30:00Z] [depth=2] assistant: hop reply";
        const wrapper = mount(RecallSearchTool, {
          props: {
            toolName: "chain_search_context",
            heading: "q",
            body,
            status: "completed",
          },
          global: { stubs: { ToolBubble } },
        });
        expect(
          wrapper.find('[data-testid="recall-timestamp"]').text(),
        ).toContain("30m ago");
        expect(wrapper.find('[data-testid="recall-chain-depth"]').text()).toBe(
          "↑2",
        );
        expect(wrapper.find('[data-testid="recall-result"]').text()).toContain(
          "hop reply",
        );
        expect(wrapper.find('[data-testid="recall-result"]').text()).toContain(
          "assistant",
        );
      } finally {
        Date.now = origNow;
      }
    });

    it("omits timestamp + chain-hop affordances when prefixes are absent (current backend format)", () => {
      // Body in the current backend wire shape — no metadata prefix.
      const body = "user: plain body, no metadata\n---\nassistant: also plain";
      const wrapper = mount(RecallSearchTool, {
        props: {
          toolName: "search_context",
          heading: "q",
          body,
          status: "completed",
        },
        global: { stubs: { ToolBubble } },
      });
      expect(wrapper.findAll('[data-testid="recall-timestamp"]')).toHaveLength(
        0,
      );
      expect(
        wrapper.findAll('[data-testid="recall-chain-depth"]'),
      ).toHaveLength(0);
      // Existing rendering path remains intact.
      const results = wrapper.findAll('[data-testid="recall-result"]');
      expect(results).toHaveLength(2);
      expect(results[0].text()).toContain("plain body, no metadata");
    });
  });
});
