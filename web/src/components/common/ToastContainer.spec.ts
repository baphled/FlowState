import { beforeEach, describe, expect, it, vi, afterEach } from "vitest";
import { mount, flushPromises } from "@vue/test-utils";
import ToastContainer from "./ToastContainer.vue";
import { showToast, useToast } from "@/composables/useToast";

function mountContainer() {
  return mount(ToastContainer, {
    attachTo: document.body,
  });
}

function qsa(selector: string): NodeListOf<HTMLElement> {
  return document.body.querySelectorAll(selector);
}

function qs(selector: string): HTMLElement | null {
  return document.body.querySelector(selector);
}

describe("ToastContainer", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    const { toasts } = useToast();
    toasts.value.splice(0, toasts.value.length);
  });

  afterEach(() => {
    vi.restoreAllMocks();
    const { toasts, dismissAll } = useToast();
    dismissAll();
    while (toasts.value.length > 0) {
      toasts.value.pop();
    }
  });

  it("renders nothing when there are no toasts", () => {
    mountContainer();

    expect(qsa('[data-testid="toast-item"]').length).toBe(0);
  });

  it("renders a toast when showToast is called", async () => {
    showToast("Hello world");
    await flushPromises();

    mountContainer();

    const toastItems = qsa('[data-testid="toast-item"]');
    expect(toastItems.length).toBe(1);
    expect(toastItems[0].textContent).toContain("Hello world");
  });

  it("renders multiple toasts stacked vertically", async () => {
    showToast("First");
    showToast("Second");
    await flushPromises();

    mountContainer();

    expect(qsa('[data-testid="toast-item"]').length).toBe(2);
  });

  it("renders toast title when provided", async () => {
    showToast({ message: "Saved", title: "Success" });
    await flushPromises();

    mountContainer();

    expect(qs('[data-testid="toast-title"]')).not.toBeNull();
    expect(qs('[data-testid="toast-title"]')!.textContent).toBe("Success");
  });

  it("does not render title element when not provided", async () => {
    showToast("No title");
    await flushPromises();

    mountContainer();

    expect(qs('[data-testid="toast-title"]')).toBeNull();
  });

  it("applies variant class to toast item", async () => {
    showToast({ message: "Error occurred", variant: "error" });
    await flushPromises();

    mountContainer();

    expect(
      qs('[data-testid="toast-item"]')!.classList.contains("toast-item--error"),
    ).toBe(true);
  });

  it("applies default variant class when no variant specified", async () => {
    showToast("Default toast");
    await flushPromises();

    mountContainer();

    expect(
      qs('[data-testid="toast-item"]')!.classList.contains(
        "toast-item--default",
      ),
    ).toBe(true);
  });

  it("applies success variant class", async () => {
    showToast({ message: "Done", variant: "success" });
    await flushPromises();

    mountContainer();

    expect(
      qs('[data-testid="toast-item"]')!.classList.contains(
        "toast-item--success",
      ),
    ).toBe(true);
  });

  it("applies loading variant class", async () => {
    showToast({ message: "Loading...", variant: "loading" });
    await flushPromises();

    mountContainer();

    expect(
      qs('[data-testid="toast-item"]')!.classList.contains(
        "toast-item--loading",
      ),
    ).toBe(true);
  });

  it("renders close button on each toast", async () => {
    showToast("Dismissible");
    await flushPromises();

    mountContainer();

    expect(qs('[data-testid="toast-close"]')).not.toBeNull();
  });

  it("dismisses toast when close button is clicked", async () => {
    const { toasts } = useToast();
    showToast("Click to close");
    await flushPromises();

    mountContainer();
    expect(qsa('[data-testid="toast-item"]').length).toBe(1);

    const closeBtn = qs('[data-testid="toast-close"]')!;
    closeBtn.click();
    await flushPromises();

    expect(qsa('[data-testid="toast-item"]').length).toBe(0);
    expect(toasts.value).toHaveLength(0);
  });

  it("renders action button when provided", async () => {
    const onClick = vi.fn();
    showToast({
      message: "Deleted",
      action: { label: "Undo", onClick },
    });
    await flushPromises();

    mountContainer();

    const actionBtn = qs('[data-testid="toast-action"]');
    expect(actionBtn).not.toBeNull();
    expect(actionBtn!.textContent).toBe("Undo");
  });

  it("calls action callback when action button is clicked", async () => {
    const onClick = vi.fn();
    showToast({
      message: "Deleted",
      action: { label: "Undo", onClick },
    });
    await flushPromises();

    mountContainer();

    const actionBtn = qs('[data-testid="toast-action"]')!;
    actionBtn.click();
    await flushPromises();

    expect(onClick).toHaveBeenCalledOnce();
  });

  it("renders the container with fixed positioning class", async () => {
    showToast("Positioned");
    await flushPromises();

    mountContainer();

    const container = qs('[data-testid="toast-container"]');
    expect(container).not.toBeNull();
    expect(container!.classList.contains("toast-container")).toBe(true);
  });

  it("reactively updates when new toasts are added after mount", async () => {
    mountContainer();

    expect(qsa('[data-testid="toast-item"]').length).toBe(0);

    showToast("Appears later");
    await flushPromises();

    expect(qsa('[data-testid="toast-item"]').length).toBe(1);
  });

  it("reactively updates when toasts are dismissed", async () => {
    const { removeToast } = useToast();
    showToast("Will be removed");
    await flushPromises();

    mountContainer();
    expect(qsa('[data-testid="toast-item"]').length).toBe(1);

    const id = useToast().toasts.value[0].id;
    removeToast(id);
    await flushPromises();

    expect(qsa('[data-testid="toast-item"]').length).toBe(0);
  });
});
