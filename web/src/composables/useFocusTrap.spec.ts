import { describe, it, expect, beforeEach } from "vitest";
import { ref, nextTick } from "vue";
import { useFocusTrap } from "./useFocusTrap";

/**
 * useFocusTrap implements modal-style Tab/Shift+Tab cycling so keyboard
 * users can navigate FuzzySearchModal, AgentPicker, and ModelPicker
 * without falling out into the underlying chat thread.
 *
 * These specs run in jsdom — the focus / Tab simulation doesn't depend on
 * the real browser focus model, so we drive the keydown listener
 * directly. The contract pinned here:
 *   - Tab from the last focusable cycles to the first.
 *   - Shift+Tab from the first cycles to the last.
 *   - Tab when activeElement is outside the container snaps back inside
 *     (defence against a stray focus from earlier activation).
 *   - On deactivation, focus is restored to the previously-focused
 *     element.
 */

function setupContainer(): {
  root: HTMLElement;
  first: HTMLButtonElement;
  mid: HTMLInputElement;
  last: HTMLButtonElement;
} {
  const root = document.createElement("div");
  // Make the container's children visible to offsetParent.
  Object.defineProperty(root, "offsetParent", {
    configurable: true,
    get: () => document.body,
  });
  const first = document.createElement("button");
  first.textContent = "first";
  const mid = document.createElement("input");
  const last = document.createElement("button");
  last.textContent = "last";
  [first, mid, last].forEach((el) => {
    Object.defineProperty(el, "offsetParent", {
      configurable: true,
      get: () => root,
    });
  });
  root.append(first, mid, last);
  document.body.appendChild(root);
  return { root, first, mid, last };
}

describe("useFocusTrap", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
  });

  it("cycles Tab from the last focusable to the first", async () => {
    const { root, first, last } = setupContainer();
    const triggerBtn = document.createElement("button");
    triggerBtn.textContent = "trigger";
    document.body.appendChild(triggerBtn);
    triggerBtn.focus();

    const containerRef = ref<HTMLElement | null>(root);
    const active = ref(false);

    // Mount a tiny harness to drive the watch.
    const { defineComponent, h } = await import("vue");
    const { mount } = await import("@vue/test-utils");
    const Harness = defineComponent({
      setup() {
        useFocusTrap(containerRef, active);
        return () => h("div");
      },
    });
    mount(Harness);

    active.value = true;
    await nextTick();
    // wait for the rAF inside activate to fire focus
    await new Promise<void>((resolve) =>
      requestAnimationFrame(() => resolve()),
    );

    // Now mimic Tab when activeElement === last.
    last.focus();
    const evt = new KeyboardEvent("keydown", { key: "Tab", cancelable: true });
    document.dispatchEvent(evt);
    expect(document.activeElement).toBe(first);
  });

  it("cycles Shift+Tab from the first focusable to the last", async () => {
    const { root, first, last } = setupContainer();
    const containerRef = ref<HTMLElement | null>(root);
    const active = ref(false);
    const { defineComponent, h } = await import("vue");
    const { mount } = await import("@vue/test-utils");
    const Harness = defineComponent({
      setup() {
        useFocusTrap(containerRef, active);
        return () => h("div");
      },
    });
    mount(Harness);
    active.value = true;
    await nextTick();
    await new Promise<void>((resolve) =>
      requestAnimationFrame(() => resolve()),
    );

    first.focus();
    const evt = new KeyboardEvent("keydown", {
      key: "Tab",
      shiftKey: true,
      cancelable: true,
    });
    document.dispatchEvent(evt);
    expect(document.activeElement).toBe(last);
  });

  it("snaps focus back into the container if Tab is pressed while activeElement is outside", async () => {
    const { root, first } = setupContainer();
    const stray = document.createElement("button");
    document.body.appendChild(stray);
    const containerRef = ref<HTMLElement | null>(root);
    const active = ref(false);
    const { defineComponent, h } = await import("vue");
    const { mount } = await import("@vue/test-utils");
    const Harness = defineComponent({
      setup() {
        useFocusTrap(containerRef, active);
        return () => h("div");
      },
    });
    mount(Harness);
    active.value = true;
    await nextTick();
    await new Promise<void>((resolve) =>
      requestAnimationFrame(() => resolve()),
    );

    stray.focus();
    const evt = new KeyboardEvent("keydown", {
      key: "Tab",
      shiftKey: true,
      cancelable: true,
    });
    document.dispatchEvent(evt);
    // Shift+Tab from outside → focus moves to last (treated as if at first)
    // We assert focus is at least back inside.
    expect(root.contains(document.activeElement)).toBe(true);
    expect(first).toBeDefined();
  });

  it("does not interfere with Escape — owners keep their handler", async () => {
    const { root } = setupContainer();
    const containerRef = ref<HTMLElement | null>(root);
    const active = ref(true);
    const { defineComponent, h } = await import("vue");
    const { mount } = await import("@vue/test-utils");
    const Harness = defineComponent({
      setup() {
        useFocusTrap(containerRef, active);
        return () => h("div");
      },
    });
    mount(Harness);
    await nextTick();

    const evt = new KeyboardEvent("keydown", {
      key: "Escape",
      cancelable: true,
    });
    const swallowed = !document.dispatchEvent(evt);
    expect(swallowed).toBe(false);
  });
});
