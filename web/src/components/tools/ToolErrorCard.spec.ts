import { describe, it, expect } from "vitest";
import { mount } from "@vue/test-utils";
import ToolErrorCard from "./ToolErrorCard.vue";

const CopyButton = {
  template: '<span data-testid="copy-btn" />',
};

describe("ToolErrorCard", () => {
  it("renders the error shell with the expected attributes", () => {
    const wrapper = mount(ToolErrorCard, {
      props: {
        toolName: "bash",
        heading: "rm -rf /tmp/cache",
        body: "Permission denied",
      },
      global: {
        stubs: {
          CopyButton,
        },
      },
    });

    const root = wrapper.get('[data-component="tool-error-card"]');
    expect(root.attributes("data-tool")).toBe("error");
    expect(wrapper.text()).toContain("✕");
    expect(wrapper.text()).toContain("bash");
    expect(wrapper.text()).toContain("rm -rf /tmp/cache");
    expect(wrapper.find('[data-component="tool-error-details"]').exists()).toBe(
      false,
    );
  });

  it("reveals error details when toggled open", async () => {
    const wrapper = mount(ToolErrorCard, {
      props: {
        toolName: "read",
        heading: "/tmp/missing.txt",
        body: "No such file or directory",
      },
      global: {
        stubs: {
          CopyButton,
        },
      },
    });

    await wrapper.get('[data-testid="tool-error-toggle"]').trigger("click");

    expect(
      wrapper.get('[data-component="tool-error-details"]').text(),
    ).toContain("No such file or directory");
    expect(wrapper.find('[data-testid="copy-btn"]').exists()).toBe(true);
  });
});
