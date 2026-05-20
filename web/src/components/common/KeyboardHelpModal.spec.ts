import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mount, flushPromises } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import KeyboardHelpModal from "./KeyboardHelpModal.vue";

// UI Parity PR2 I2 (May 2026) — discoverable keyboard shortcut list.
// The modal is purely presentational: it renders a list of bindings and
// emits 'close' on Escape, backdrop click, and the X button. These
// specs pin the open/closed contract and the emit shape so consumers
// (ChatView's keyboardHelpOpen state) can rely on it.
describe("KeyboardHelpModal", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("does NOT render when open=false", () => {
    const wrapper = mount(KeyboardHelpModal, { props: { open: false } });
    expect(wrapper.find('[data-testid="keyboard-help-modal"]').exists()).toBe(
      false,
    );
  });

  it("renders the modal panel when open=true", () => {
    const wrapper = mount(KeyboardHelpModal, { props: { open: true } });
    expect(wrapper.find('[data-testid="keyboard-help-modal"]').exists()).toBe(
      true,
    );
    // At least one group title rendered — the modal is not a stub.
    expect(
      wrapper.find('[data-testid="keyboard-help-group-composer"]').exists(),
    ).toBe(true);
    expect(
      wrapper
        .find('[data-testid="keyboard-help-group-streaming-control"]')
        .exists(),
    ).toBe(true);
  });

  it("emits close on the X button", async () => {
    const wrapper = mount(KeyboardHelpModal, { props: { open: true } });
    await wrapper.get('[data-testid="keyboard-help-close"]').trigger("click");
    expect(wrapper.emitted("close")).toBeTruthy();
    expect(wrapper.emitted("close")?.length).toBe(1);
  });

  it("emits close on the backdrop click", async () => {
    const wrapper = mount(KeyboardHelpModal, { props: { open: true } });
    await wrapper
      .get('[data-testid="keyboard-help-backdrop"]')
      .trigger("click");
    expect(wrapper.emitted("close")).toBeTruthy();
  });

  it("emits close on Escape keypress while open", async () => {
    const wrapper = mount(KeyboardHelpModal, {
      props: { open: true },
      attachTo: document.body,
    });
    await flushPromises();
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    await flushPromises();
    expect(wrapper.emitted("close")).toBeTruthy();
    wrapper.unmount();
  });

  it("lists the Escape-twice cancel binding (B5 / Slice G discoverability)", () => {
    const wrapper = mount(KeyboardHelpModal, { props: { open: true } });
    // The streaming-control group must surface the Esc-Esc cancel
    // affordance — the brief calls out this binding as undiscoverable.
    const html = wrapper.html();
    expect(html).toMatch(/Esc.*Esc/s);
    expect(html).toMatch(/Cancel in-flight turn/i);
  });

  it("lists ArrowUp prompt-recall (B4 binding)", () => {
    const wrapper = mount(KeyboardHelpModal, { props: { open: true } });
    const html = wrapper.html();
    expect(html).toMatch(/Recall previous prompt/i);
  });
});
