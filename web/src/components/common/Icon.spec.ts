import { describe, expect, it } from "vitest";
import { mount } from "@vue/test-utils";
import Icon from "./Icon.vue";

// UI Parity PR2 I1 (May 2026) — Icon wrapper around lucide-vue-next.
// Pin the wrapper's API contract so consumer call sites do not regress:
//   - the SVG renders with currentColor (theme-propagation property)
//   - data-icon-name reflects the semantic prop
//   - ariaLabel surfaces as role="img" + aria-label (a11y contract)
//   - missing ariaLabel falls back to aria-hidden="true" (decorative)
describe("Icon wrapper (I1)", () => {
  it("renders an SVG with the data-icon-name attribute", () => {
    const wrapper = mount(Icon, { props: { name: "trash" } });
    const el = wrapper.find('[data-testid="icon"]').element as SVGElement;
    expect(el).toBeTruthy();
    expect(el.getAttribute("data-icon-name")).toBe("trash");
  });

  it("is decorative by default (aria-hidden, no role=img)", () => {
    const wrapper = mount(Icon, { props: { name: "plus" } });
    const el = wrapper.find('[data-testid="icon"]').element;
    expect(el.getAttribute("aria-hidden")).toBe("true");
    expect(el.getAttribute("role")).toBe(null);
  });

  it("promotes to role=img when ariaLabel is supplied", () => {
    const wrapper = mount(Icon, {
      props: { name: "trash", ariaLabel: "Delete session" },
    });
    const el = wrapper.find('[data-testid="icon"]').element;
    expect(el.getAttribute("role")).toBe("img");
    expect(el.getAttribute("aria-label")).toBe("Delete session");
    expect(el.getAttribute("aria-hidden")).toBe(null);
  });

  it("respects the size prop for both width and height", () => {
    const wrapper = mount(Icon, { props: { name: "message", size: 24 } });
    const el = wrapper.find('[data-testid="icon"]').element;
    // Lucide forwards size to width + height.
    expect(el.getAttribute("width")).toBe("24");
    expect(el.getAttribute("height")).toBe("24");
  });

  // Theme parity — the scoped style sets color: currentColor on the
  // .icon class. Verify the class is applied so any consumer CSS rule
  // styling `color:` propagates to the stroke.
  it("applies the .icon class so currentColor propagates", () => {
    const wrapper = mount(Icon, { props: { name: "bot" } });
    const el = wrapper.find('[data-testid="icon"]').element;
    expect(el.classList.contains("icon")).toBe(true);
  });

  it("renders every catalogue name without throwing", () => {
    const names = [
      "message",
      "plus",
      "trash",
      "search",
      "bot",
      "document",
      "clock",
      "inbox",
      "stop",
      "attach",
      "close",
    ] as const;
    for (const name of names) {
      const wrapper = mount(Icon, { props: { name } });
      expect(wrapper.find('[data-testid="icon"]').exists()).toBe(true);
      wrapper.unmount();
    }
  });
});
