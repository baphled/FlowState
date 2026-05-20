import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { mount, flushPromises } from "@vue/test-utils";
import { setActivePinia, createPinia } from "pinia";
import ModelSwitcher from "./ModelSwitcher.vue";
import * as api from "@/api";
import { useChatStore } from "@/stores/chatStore";
import type { ModelsResponse } from "@/types";

function makeModelsResponse(): ModelsResponse {
  return {
    providers: [
      {
        id: "anthropic",
        models: [
          { id: "claude-opus-4", name: "Claude Opus 4" },
          { id: "claude-sonnet-4", name: "Claude Sonnet 4" },
        ],
      },
      {
        id: "openai",
        models: [{ id: "gpt-4o", name: "GPT-4o" }],
      },
    ],
  };
}

describe("ModelSwitcher", () => {
  let listModelsSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    setActivePinia(createPinia());
    listModelsSpy = vi
      .spyOn(api, "listModels")
      .mockResolvedValue(makeModelsResponse());
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("calls api.listModels on mount and caches the result", async () => {
    const wrapper = mount(ModelSwitcher);
    await flushPromises();

    expect(listModelsSpy).toHaveBeenCalledTimes(1);

    await wrapper
      .find('[data-testid="model-switcher-trigger"]')
      .trigger("click");
    await wrapper
      .find('[data-testid="model-switcher-trigger"]')
      .trigger("click");
    await flushPromises();

    expect(listModelsSpy).toHaveBeenCalledTimes(1);
  });

  it("shows the placeholder text when no model is selected", async () => {
    const wrapper = mount(ModelSwitcher);
    await flushPromises();

    expect(
      wrapper.find('[data-testid="model-switcher-trigger"]').text(),
    ).toContain("Select model");
  });

  it("displays the current selection as provider/model", async () => {
    const store = useChatStore();
    store.currentProviderId = "anthropic";
    store.currentModelId = "claude-opus-4";

    const wrapper = mount(ModelSwitcher);
    await flushPromises();

    const trigger = wrapper.find('[data-testid="model-switcher-trigger"]');
    expect(trigger.text()).toContain("anthropic/claude-opus-4");
  });

  it("opens the dropdown listing providers and their models on click", async () => {
    const wrapper = mount(ModelSwitcher);
    await flushPromises();

    expect(
      wrapper.find('[data-testid="model-switcher-dropdown"]').exists(),
    ).toBe(false);

    await wrapper
      .find('[data-testid="model-switcher-trigger"]')
      .trigger("click");

    const dropdown = wrapper.find('[data-testid="model-switcher-dropdown"]');
    expect(dropdown.exists()).toBe(true);
    expect(dropdown.text()).toContain("anthropic");
    expect(dropdown.text()).toContain("Claude Opus 4");
    expect(dropdown.text()).toContain("Claude Sonnet 4");
    expect(dropdown.text()).toContain("openai");
    expect(dropdown.text()).toContain("GPT-4o");
  });

  it("closes the dropdown when clicking outside", async () => {
    const wrapper = mount(ModelSwitcher, { attachTo: document.body });
    await flushPromises();
    await wrapper
      .find('[data-testid="model-switcher-trigger"]')
      .trigger("click");
    expect(
      wrapper.find('[data-testid="model-switcher-dropdown"]').exists(),
    ).toBe(true);

    document.body.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
    await flushPromises();

    expect(
      wrapper.find('[data-testid="model-switcher-dropdown"]').exists(),
    ).toBe(false);
    wrapper.unmount();
  });

  it("closes the dropdown when Escape is pressed", async () => {
    const wrapper = mount(ModelSwitcher, { attachTo: document.body });
    await flushPromises();
    await wrapper
      .find('[data-testid="model-switcher-trigger"]')
      .trigger("click");
    expect(
      wrapper.find('[data-testid="model-switcher-dropdown"]').exists(),
    ).toBe(true);

    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    await flushPromises();

    expect(
      wrapper.find('[data-testid="model-switcher-dropdown"]').exists(),
    ).toBe(false);
    wrapper.unmount();
  });

  it("calls chatStore.setModel with split provider and model ids when an option is selected", async () => {
    const store = useChatStore();
    const setModelSpy = vi.spyOn(store, "setModel").mockResolvedValue();

    const wrapper = mount(ModelSwitcher);
    await flushPromises();
    await wrapper
      .find('[data-testid="model-switcher-trigger"]')
      .trigger("click");

    const option = wrapper.find(
      '[data-testid="model-option-anthropic-claude-opus-4"]',
    );
    expect(option.exists()).toBe(true);
    await option.trigger("click");

    expect(setModelSpy).toHaveBeenCalledWith("claude-opus-4", "anthropic");
    expect(
      wrapper.find('[data-testid="model-switcher-dropdown"]').exists(),
    ).toBe(false);
  });

  it("shows a loading state while fetching models", async () => {
    let resolveFn: (value: ModelsResponse) => void = () => {};
    listModelsSpy.mockReturnValueOnce(
      new Promise<ModelsResponse>((resolve) => {
        resolveFn = resolve;
      }),
    );

    const wrapper = mount(ModelSwitcher);
    await wrapper.vm.$nextTick();

    expect(
      wrapper.find('[data-testid="model-switcher-loading"]').exists(),
    ).toBe(true);

    resolveFn(makeModelsResponse());
    await flushPromises();

    expect(
      wrapper.find('[data-testid="model-switcher-loading"]').exists(),
    ).toBe(false);
  });

  it("shows an error state with a retry button when listModels fails, and retries on click", async () => {
    listModelsSpy.mockRejectedValueOnce(new Error("network down"));

    const wrapper = mount(ModelSwitcher);
    await flushPromises();

    const errorEl = wrapper.find('[data-testid="model-switcher-error"]');
    expect(errorEl.exists()).toBe(true);
    expect(errorEl.text()).toContain("network down");

    listModelsSpy.mockResolvedValueOnce(makeModelsResponse());
    await wrapper.find('[data-testid="model-switcher-retry"]').trigger("click");
    await flushPromises();

    expect(wrapper.find('[data-testid="model-switcher-error"]').exists()).toBe(
      false,
    );
    expect(listModelsSpy).toHaveBeenCalledTimes(2);
  });

  it("reactively updates the trigger label when chatStore current selection changes", async () => {
    const store = useChatStore();
    const wrapper = mount(ModelSwitcher);
    await flushPromises();

    expect(
      wrapper.find('[data-testid="model-switcher-trigger"]').text(),
    ).toContain("Select model");

    store.currentProviderId = "openai";
    store.currentModelId = "gpt-4o";
    await wrapper.vm.$nextTick();

    expect(
      wrapper.find('[data-testid="model-switcher-trigger"]').text(),
    ).toContain("openai/gpt-4o");
  });
});
