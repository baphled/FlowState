import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import PermissionModeChip from "./PermissionModeChip.vue";
import { useChatStore } from "@/stores/chatStore";

/**
 * PermissionModeChip component specs — pin behaviour observable to a
 * user. Slice 2 of the Permission Modes (May 2026) plan.
 *
 * Focal assertions per the plan's acceptance criteria:
 *
 *   - Chip renders the current mode label sourced from the store.
 *   - Severity attribute pivots on the active mode so the colour
 *     palette can be probed without coupling to specific RGB values.
 *   - Clicking the chip opens the popover with all four modes listed
 *     in the canonical closed-vocabulary order.
 *   - Selecting a mode calls store.setPermissionMode with the
 *     identifier.
 *   - The loud-disclosure paragraph for Default mode is rendered in
 *     the DOM when the popover is open, with the literal text the
 *     plan §5 specifies. This is the v1 mitigation for the Default
 *     "no per-call prompt" gap and the user-explicit
 *     `feedback_dont_defer_violations_of_stated_intent` rule
 *     requires it be visible at the decision point — not buried
 *     in hover-only state.
 *
 * Memory gotchas honoured:
 *   - `feedback_pinia_onmounted_clobbers_seed` — the chip has no
 *     onMounted async load so post-mount seeding is unnecessary,
 *     but the `flushPromises` discipline is observed below to keep
 *     the spec resilient if hydration is moved into onMounted later.
 *   - `feedback_response_ok_mock_gotcha` — Slice 2 makes no fetch
 *     calls so no `ok` mock is needed here. Slice 3 will add fetch
 *     and the mock contract grows with it.
 */

function installLocalStorageStub(): void {
  const store = new Map<string, string>();
  const stub = {
    getItem: (key: string) => (store.has(key) ? store.get(key)! : null),
    setItem: (key: string, value: string) => {
      store.set(key, String(value));
    },
    removeItem: (key: string) => {
      store.delete(key);
    },
    clear: () => store.clear(),
    key: (i: number) => Array.from(store.keys())[i] ?? null,
    get length() {
      return store.size;
    },
  };
  Object.defineProperty(window, "localStorage", {
    value: stub,
    configurable: true,
  });
}

describe("PermissionModeChip", () => {
  beforeEach(() => {
    installLocalStorageStub();
    vi.clearAllMocks();
    setActivePinia(createPinia());
  });

  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("renders the current mode label sourced from the store", async () => {
    const store = useChatStore();
    store.currentSessionId = "session-1";
    store.permissionMode = "default";

    const wrapper = mount(PermissionModeChip);
    await flushPromises();

    expect(
      wrapper.get('[data-testid="permission-mode-chip-label"]').text(),
    ).toBe("Default");
    expect(
      wrapper.get('[data-testid="permission-mode-chip"]').attributes("data-mode"),
    ).toBe("default");
  });

  it("reflects store changes reactively after mount", async () => {
    const store = useChatStore();
    store.currentSessionId = "session-1";
    store.permissionMode = "default";

    const wrapper = mount(PermissionModeChip);
    await flushPromises();

    // Mutate the store after mount (mirrors the click → setPermissionMode
    // path without going through the popover). Reactivity must surface
    // the new mode on the chip's label and severity attribute.
    store.permissionMode = "yolo";
    await flushPromises();

    expect(
      wrapper.get('[data-testid="permission-mode-chip-label"]').text(),
    ).toBe("YOLO");
    expect(
      wrapper.get('[data-testid="permission-mode-chip"]').attributes("data-mode"),
    ).toBe("yolo");
    expect(
      wrapper.get('[data-testid="permission-mode-chip"]').attributes("data-severity"),
    ).toBe("danger");
  });

  it("opens the popover with the four canonical modes when clicked", async () => {
    const store = useChatStore();
    store.currentSessionId = "session-1";

    const wrapper = mount(PermissionModeChip);
    await flushPromises();

    // Resting state — popover absent.
    expect(
      wrapper.find('[data-testid="permission-mode-chip-popover"]').exists(),
    ).toBe(false);

    await wrapper.get('[data-testid="permission-mode-chip"]').trigger("click");
    await flushPromises();

    const popover = wrapper.find('[data-testid="permission-mode-chip-popover"]');
    expect(popover.exists()).toBe(true);

    // All four canonical modes present, each as its own option button.
    expect(wrapper.find('[data-testid="permission-mode-option-plan"]').exists())
      .toBe(true);
    expect(wrapper.find('[data-testid="permission-mode-option-default"]').exists())
      .toBe(true);
    expect(
      wrapper.find('[data-testid="permission-mode-option-accept_edits"]').exists(),
    ).toBe(true);
    expect(wrapper.find('[data-testid="permission-mode-option-yolo"]').exists())
      .toBe(true);
  });

  it("selecting a mode calls store.setPermissionMode and closes the popover", async () => {
    const store = useChatStore();
    store.currentSessionId = "session-1";
    store.permissionMode = "default";

    const setMode = vi.spyOn(store, "setPermissionMode");

    const wrapper = mount(PermissionModeChip);
    await flushPromises();

    await wrapper.get('[data-testid="permission-mode-chip"]').trigger("click");
    await flushPromises();

    await wrapper
      .get('[data-testid="permission-mode-option-yolo"]')
      .trigger("click");
    await flushPromises();

    expect(setMode).toHaveBeenCalledWith("yolo");
    // Popover closes on selection so the chip returns to the resting
    // pill — the operator made their pick, the surface dismisses.
    expect(
      wrapper.find('[data-testid="permission-mode-chip-popover"]').exists(),
    ).toBe(false);
  });

  it("renders the loud-disclosure paragraph for Default mode when the popover is open", async () => {
    const store = useChatStore();
    store.currentSessionId = "session-1";

    const wrapper = mount(PermissionModeChip);
    await flushPromises();

    // Popover closed — disclosure absent from the DOM. This pins the
    // requirement that the disclosure is gated to the popover (not
    // leaked into the resting chip's chrome).
    expect(
      wrapper.find('[data-testid="permission-mode-default-disclosure"]').exists(),
    ).toBe(false);

    await wrapper.get('[data-testid="permission-mode-chip"]').trigger("click");
    await flushPromises();

    const disclosure = wrapper.get(
      '[data-testid="permission-mode-default-disclosure"]',
    );
    expect(disclosure.exists()).toBe(true);
    // Literal text per plan §5 — pinning the exact copy guards against
    // a future restyle silently weakening the disclosure.
    expect(disclosure.text()).toBe(
      "Default mode does not prompt per tool call. Review the session timeline for what ran.",
    );
  });

  it("ticks the active mode option in the popover so the operator sees current selection", async () => {
    const store = useChatStore();
    store.currentSessionId = "session-1";
    store.permissionMode = "plan";

    const wrapper = mount(PermissionModeChip);
    await flushPromises();

    await wrapper.get('[data-testid="permission-mode-chip"]').trigger("click");
    await flushPromises();

    const planOption = wrapper.get('[data-testid="permission-mode-option-plan"]');
    expect(planOption.attributes("aria-selected")).toBe("true");
    expect(planOption.classes()).toContain(
      "permission-mode-chip__option--active",
    );

    const yoloOption = wrapper.get('[data-testid="permission-mode-option-yolo"]');
    expect(yoloOption.attributes("aria-selected")).toBe("false");
  });
});
