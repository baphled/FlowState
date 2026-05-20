import { describe, it, expect, beforeEach } from "vitest";
import { mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";

import GateFailureBanner from "./GateFailureBanner.vue";
import { useChatStore } from "@/stores/chatStore";

/**
 * Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026):
 * GateFailureBanner is the persistent affordance for the
 * `gate_failed` SSE event class. The Go SSE pipeline emits
 * `{"type":"gate_failed", swarm_id, lifecycle, member_id, gate_name,
 *  gate_kind, reason, cause, coord_store_keys}` when the engine's
 * runSwarmGates / dispatchMemberGates halts on a *swarm.GateError.
 * The chat store routes this to `lastGateFailure`; this banner
 * binds to that state.
 *
 * Visual contract (mirrors CriticalErrorBanner persistent shape):
 *   - Anchored above the message pane so the user sees the halt
 *     before scrolling.
 *   - Severity palette matching the existing critical banner.
 *   - role="alert" so screen readers announce on arrival.
 *   - Persists until either Dismiss click or session change (the
 *     store handles the session-change clear).
 *
 * Affordances pinned by the specs below:
 *   - Title: "Swarm gate halted: <gate_name>"
 *   - Body: <reason> (+ <cause> when present)
 *   - Subtitle: "<lifecycle> gate on <member_id> in swarm <swarm_id>"
 *   - "What was checked?" expander surfacing coord_store_keys
 *   - Dismiss button calls chatStore.clearGateFailure
 */
describe("GateFailureBanner", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  it("hides when lastGateFailure is null", () => {
    const wrapper = mount(GateFailureBanner);
    expect(wrapper.find('[data-testid="gate-failure-banner"]').exists()).toBe(
      false,
    );
  });

  it("renders with the failing gate name in the title when lastGateFailure is populated", () => {
    const store = useChatStore();
    store.lastGateFailure = {
      swarmId: "a-team",
      lifecycle: "post-member",
      memberId: "researcher",
      gateName: "post-member-researcher-relevance-gate",
      gateKind: "ext:relevance-gate",
      reason: "off-topic",
      cause: "score 0.31 < threshold 0.5",
      coordStoreKeys: ["chain/researcher/output", "chain/topic/spec"],
    };

    const wrapper = mount(GateFailureBanner);
    expect(wrapper.find('[data-testid="gate-failure-banner"]').exists()).toBe(
      true,
    );
    const title = wrapper.find('[data-testid="gate-failure-title"]');
    expect(title.exists()).toBe(true);
    expect(title.text()).toContain("Swarm gate halted");
    expect(title.text()).toContain("post-member-researcher-relevance-gate");
  });

  it("renders the typed Reason as the banner body", () => {
    const store = useChatStore();
    store.lastGateFailure = {
      swarmId: "a-team",
      lifecycle: "post-member",
      memberId: "researcher",
      gateName: "post-member-researcher-relevance-gate",
      gateKind: "ext:relevance-gate",
      reason: "off-topic",
      cause: "",
      coordStoreKeys: [],
    };

    const wrapper = mount(GateFailureBanner);
    const message = wrapper.find('[data-testid="gate-failure-message"]');
    expect(message.exists()).toBe(true);
    expect(message.text()).toContain("off-topic");
  });

  it("renders the wrapped cause alongside the reason when populated", () => {
    const store = useChatStore();
    store.lastGateFailure = {
      swarmId: "a-team",
      lifecycle: "pre",
      memberId: "",
      gateName: "envelope-check",
      gateKind: "builtin:result-schema",
      reason: "schema validation failed",
      cause: 'missing required property "verdict"',
      coordStoreKeys: [],
    };

    const wrapper = mount(GateFailureBanner);
    const cause = wrapper.find('[data-testid="gate-failure-cause"]');
    expect(cause.exists()).toBe(true);
    expect(cause.text()).toContain('missing required property "verdict"');
  });

  it("renders the lifecycle / member / swarm subtitle for member-scoped halts", () => {
    const store = useChatStore();
    store.lastGateFailure = {
      swarmId: "a-team",
      lifecycle: "post-member",
      memberId: "researcher",
      gateName: "post-member-researcher-relevance-gate",
      gateKind: "ext:relevance-gate",
      reason: "off-topic",
      cause: "",
      coordStoreKeys: [],
    };

    const wrapper = mount(GateFailureBanner);
    const subtitle = wrapper.find('[data-testid="gate-failure-subtitle"]');
    expect(subtitle.exists()).toBe(true);
    expect(subtitle.text()).toContain("post-member");
    expect(subtitle.text()).toContain("researcher");
    expect(subtitle.text()).toContain("a-team");
  });

  it('omits the member from the subtitle for swarm-level halts (lifecycle "pre" / "post")', () => {
    const store = useChatStore();
    store.lastGateFailure = {
      swarmId: "a-team",
      lifecycle: "pre",
      memberId: "",
      gateName: "envelope-check",
      gateKind: "builtin:result-schema",
      reason: "schema validation failed",
      cause: "",
      coordStoreKeys: [],
    };

    const wrapper = mount(GateFailureBanner);
    const subtitle = wrapper.find('[data-testid="gate-failure-subtitle"]');
    expect(subtitle.exists()).toBe(true);
    expect(subtitle.text()).toContain("pre");
    expect(subtitle.text()).toContain("a-team");
    // No member in the subtitle when memberId is empty.
    expect(subtitle.text()).not.toContain("researcher");
  });

  it('exposes a "what was checked?" expander surfacing coord_store_keys when populated', async () => {
    const store = useChatStore();
    store.lastGateFailure = {
      swarmId: "a-team",
      lifecycle: "post-member",
      memberId: "researcher",
      gateName: "post-member-researcher-relevance-gate",
      gateKind: "ext:relevance-gate",
      reason: "off-topic",
      cause: "",
      coordStoreKeys: ["chain/researcher/output", "chain/topic/spec"],
    };

    const wrapper = mount(GateFailureBanner);
    const toggle = wrapper.find('[data-testid="gate-failure-details-toggle"]');
    expect(toggle.exists()).toBe(true);

    await toggle.trigger("click");

    const details = wrapper.find('[data-testid="gate-failure-details"]');
    expect(details.exists()).toBe(true);
    expect(details.text()).toContain("chain/researcher/output");
    expect(details.text()).toContain("chain/topic/spec");
  });

  it('hides the "what was checked?" expander when coord_store_keys is empty (legacy single-key gates)', () => {
    const store = useChatStore();
    store.lastGateFailure = {
      swarmId: "a-team",
      lifecycle: "pre",
      memberId: "",
      gateName: "envelope-check",
      gateKind: "builtin:result-schema",
      reason: "schema validation failed",
      cause: "",
      coordStoreKeys: [],
    };

    const wrapper = mount(GateFailureBanner);
    expect(
      wrapper.find('[data-testid="gate-failure-details-toggle"]').exists(),
    ).toBe(false);
  });

  it("Dismiss button clears lastGateFailure via the store action", async () => {
    const store = useChatStore();
    store.lastGateFailure = {
      swarmId: "a-team",
      lifecycle: "post-member",
      memberId: "researcher",
      gateName: "post-member-researcher-relevance-gate",
      gateKind: "ext:relevance-gate",
      reason: "off-topic",
      cause: "",
      coordStoreKeys: [],
    };

    const wrapper = mount(GateFailureBanner);
    expect(wrapper.find('[data-testid="gate-failure-banner"]').exists()).toBe(
      true,
    );

    await wrapper.find('[data-testid="gate-failure-dismiss"]').trigger("click");

    expect(store.lastGateFailure).toBeNull();
  });

  it('uses role="alert" with aria-live="assertive" for screen-reader announcement on arrival', () => {
    const store = useChatStore();
    store.lastGateFailure = {
      swarmId: "a-team",
      lifecycle: "post-member",
      memberId: "researcher",
      gateName: "post-member-researcher-relevance-gate",
      gateKind: "ext:relevance-gate",
      reason: "off-topic",
      cause: "",
      coordStoreKeys: [],
    };

    const wrapper = mount(GateFailureBanner);
    const banner = wrapper.find('[data-testid="gate-failure-banner"]');
    expect(banner.attributes("role")).toBe("alert");
    expect(banner.attributes("aria-live")).toBe("assertive");
  });
});
