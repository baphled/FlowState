import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import PermissionPrompt from "./PermissionPrompt.vue";
import { useChatStore } from "@/stores/chatStore";

/**
 * PermissionPrompt component specs — Permission Mode ModeAskUser
 * Extension plan (May 2026), Slice 3, §3.
 *
 * Focal assertions per the plan's acceptance criteria:
 *
 *   - Four scope buttons render: Allow once / This session / Forever /
 *     Deny — exact-case labels, exact-case scope values when emitted.
 *   - Each button click routes through chatStore.grantPermission with
 *     the matching scope string.
 *   - All four visual rows (Tool / Resource / Agent / Reason) render
 *     when the corresponding fields are present.
 *   - "Granting…" state surfaces on every button while a grant is in
 *     flight; buttons are disabled during that window.
 *   - POST failure clears the Granting… state (re-enables buttons)
 *     and surfaces a local error message.
 *
 * Memory gotchas honoured:
 *   - `feedback_response_ok_mock_gotcha` — every fetch path goes
 *     through the chatStore action mocked elsewhere; this spec stubs
 *     `chatStore.grantPermission` directly so no fetch shape matters.
 *   - `feedback_pinia_onmounted_clobbers_seed` — the component has no
 *     onMounted async work; pre-mount seeding is safe. Tests also
 *     run `flushPromises` post-mount to keep the spec resilient to a
 *     future onMounted addition.
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

const baseRequest = {
  request_id: "req-1",
  tool_name: "read",
  agent_name: "coordinator",
  resource: "/home/baphled/secret.txt",
  denial_reason: "access denied by 'read' permissions",
  mode: "ask",
  status: "pending" as const,
};

describe("PermissionPrompt", () => {
  beforeEach(() => {
    installLocalStorageStub();
    vi.clearAllMocks();
    setActivePinia(createPinia());
  });

  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("renders all four visual rows (Tool / Resource / Agent / Reason)", async () => {
    const wrapper = mount(PermissionPrompt, {
      props: { request: baseRequest },
    });
    await flushPromises();

    expect(
      wrapper.get('[data-testid="permission-prompt-tool"]').text(),
    ).toBe("read");
    expect(
      wrapper.get('[data-testid="permission-prompt-resource"]').text(),
    ).toBe("/home/baphled/secret.txt");
    expect(
      wrapper.get('[data-testid="permission-prompt-agent"]').text(),
    ).toBe("coordinator");
    expect(
      wrapper.get('[data-testid="permission-prompt-reason"]').text(),
    ).toBe("access denied by 'read' permissions");
  });

  it("omits optional rows when the corresponding fields are absent", async () => {
    const minimal = {
      request_id: "req-min",
      tool_name: "bash",
      status: "pending" as const,
    };
    const wrapper = mount(PermissionPrompt, {
      props: { request: minimal },
    });
    await flushPromises();

    expect(wrapper.get('[data-testid="permission-prompt-tool"]').text()).toBe("bash");
    expect(wrapper.find('[data-testid="permission-prompt-resource"]').exists()).toBe(
      false,
    );
    expect(wrapper.find('[data-testid="permission-prompt-agent"]').exists()).toBe(
      false,
    );
    expect(wrapper.find('[data-testid="permission-prompt-reason"]').exists()).toBe(
      false,
    );
  });

  it("renders the four scope buttons with the canonical labels", async () => {
    const wrapper = mount(PermissionPrompt, {
      props: { request: baseRequest },
    });
    await flushPromises();

    expect(
      wrapper.get('[data-testid="permission-prompt-allow-once"]').text(),
    ).toBe("Allow once");
    expect(
      wrapper.get('[data-testid="permission-prompt-allow-session"]').text(),
    ).toBe("This session");
    expect(
      wrapper.get('[data-testid="permission-prompt-allow-forever"]').text(),
    ).toBe("Forever");
    expect(
      wrapper.get('[data-testid="permission-prompt-deny"]').text(),
    ).toBe("Deny");
  });

  it("calls chatStore.grantPermission with scope='once' on Allow once click", async () => {
    const store = useChatStore();
    const grantSpy = vi
      .spyOn(store, "grantPermission")
      .mockResolvedValueOnce(undefined);

    const wrapper = mount(PermissionPrompt, {
      props: { request: baseRequest },
    });
    await flushPromises();

    await wrapper.get('[data-testid="permission-prompt-allow-once"]').trigger("click");
    await flushPromises();

    expect(grantSpy).toHaveBeenCalledTimes(1);
    expect(grantSpy).toHaveBeenCalledWith("req-1", "once");
  });

  it("calls chatStore.grantPermission with the correct scope for each button", async () => {
    const store = useChatStore();
    const grantSpy = vi
      .spyOn(store, "grantPermission")
      .mockResolvedValue(undefined);

    const wrapper = mount(PermissionPrompt, {
      props: { request: baseRequest },
    });
    await flushPromises();

    await wrapper.get('[data-testid="permission-prompt-allow-session"]').trigger("click");
    await flushPromises();
    expect(grantSpy).toHaveBeenLastCalledWith("req-1", "session");

    await wrapper.get('[data-testid="permission-prompt-allow-forever"]').trigger("click");
    await flushPromises();
    expect(grantSpy).toHaveBeenLastCalledWith("req-1", "forever");

    await wrapper.get('[data-testid="permission-prompt-deny"]').trigger("click");
    await flushPromises();
    expect(grantSpy).toHaveBeenLastCalledWith("req-1", "deny");
  });

  it("shows 'Granting…' on every button while the request is in flight", async () => {
    const store = useChatStore();
    // Seed the optimistic flag BEFORE mount — the component reads
    // chatStore.grantingPermissionRequests.has(request_id) reactively
    // so the initial render already shows the in-flight label.
    store.grantingPermissionRequests.add("req-1");

    const wrapper = mount(PermissionPrompt, {
      props: { request: baseRequest },
    });
    await flushPromises();

    expect(
      wrapper.get('[data-testid="permission-prompt-allow-once"]').text(),
    ).toBe("Granting…");
    expect(
      wrapper.get('[data-testid="permission-prompt-allow-session"]').text(),
    ).toBe("Granting…");
    expect(
      wrapper.get('[data-testid="permission-prompt-allow-forever"]').text(),
    ).toBe("Granting…");
    expect(
      wrapper.get('[data-testid="permission-prompt-deny"]').text(),
    ).toBe("Granting…");

    // Buttons MUST be disabled during the in-flight window so a
    // double-click does not POST twice. The store action's optimistic
    // flag is the source of truth; this is the DOM-side affordance
    // that screen readers + keyboard nav pick up.
    expect(
      wrapper
        .get('[data-testid="permission-prompt-allow-once"]')
        .attributes("disabled"),
    ).toBeDefined();
  });

  it("surfaces the POST failure inline and clears the Granting… state", async () => {
    const store = useChatStore();
    // The actual store action's catch path clears the optimistic
    // flag AND re-throws. The prompt's local error slot then captures
    // the message and renders it inline.
    const grantSpy = vi
      .spyOn(store, "grantPermission")
      .mockRejectedValueOnce(new Error("permission request not found"));

    const wrapper = mount(PermissionPrompt, {
      props: { request: baseRequest },
    });
    await flushPromises();

    await wrapper.get('[data-testid="permission-prompt-allow-once"]').trigger("click");
    await flushPromises();

    expect(grantSpy).toHaveBeenCalledTimes(1);
    expect(
      wrapper.get('[data-testid="permission-prompt-error"]').text(),
    ).toContain("permission request not found");
    // Granting… cleared (the store action does this itself on failure)
    // → buttons re-enabled for retry.
    expect(store.grantingPermissionRequests.has("req-1")).toBe(false);
    expect(
      wrapper.get('[data-testid="permission-prompt-allow-once"]').text(),
    ).toBe("Allow once");
  });

  it("ignores a second click while a grant is already in flight", async () => {
    const store = useChatStore();
    const grantSpy = vi
      .spyOn(store, "grantPermission")
      .mockImplementation(() => new Promise(() => undefined));

    // Pre-flag the in-flight state so the component's gate fires on
    // first click without us having to time the resolution.
    store.grantingPermissionRequests.add("req-1");

    const wrapper = mount(PermissionPrompt, {
      props: { request: baseRequest },
    });
    await flushPromises();

    await wrapper.get('[data-testid="permission-prompt-allow-once"]').trigger("click");
    await flushPromises();

    expect(grantSpy).not.toHaveBeenCalled();
  });
});
