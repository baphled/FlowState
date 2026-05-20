import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { nextTick } from "vue";
import { mount } from "@vue/test-utils";
import CopyButton from "./CopyButton.vue";

describe("CopyButton", () => {
  const writeText = vi.fn();

  beforeEach(() => {
    vi.useFakeTimers();
    writeText.mockReset();
    vi.stubGlobal("navigator", {
      clipboard: {
        writeText,
      },
    } as unknown as Navigator);
  });

  afterEach(() => {
    vi.runOnlyPendingTimers();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("renders the copy button with test id", () => {
    const wrapper = mount(CopyButton, {
      props: {
        text: "hello",
      },
    });

    expect(wrapper.find('[data-testid="copy-btn"]').exists()).toBe(true);
  });

  it("copies the provided text when clicked", async () => {
    writeText.mockResolvedValueOnce(undefined);
    const wrapper = mount(CopyButton, {
      props: {
        text: "hello world",
      },
    });

    await wrapper.get('[data-testid="copy-btn"]').trigger("click");

    expect(writeText).toHaveBeenCalledWith("hello world");
  });

  it("shows the check icon after a successful copy", async () => {
    writeText.mockResolvedValueOnce(undefined);
    const wrapper = mount(CopyButton, {
      props: {
        text: "hello world",
      },
    });

    await wrapper.get('[data-testid="copy-btn"]').trigger("click");

    expect(wrapper.text()).toContain("✓");
  });

  it("returns to the copy icon after the timeout", async () => {
    writeText.mockResolvedValueOnce(undefined);
    const wrapper = mount(CopyButton, {
      props: {
        text: "hello world",
      },
    });

    await wrapper.get('[data-testid="copy-btn"]').trigger("click");
    expect(wrapper.text()).toContain("✓");

    vi.advanceTimersByTime(2000);
    await nextTick();

    expect(wrapper.text()).toContain("📋");
  });
});
