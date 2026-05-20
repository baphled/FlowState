import { setActivePinia, createPinia } from "pinia";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useChatStore } from "@/stores/chatStore";
import {
  HIERARCHY_NAV_CHORD_TIMEOUT_MS,
  installSessionHierarchyNav,
} from "./useSessionHierarchyNav";

// useSessionHierarchyNav installs a single document-level keydown listener
// that:
//   - Up                  → loads the parent of the current child session
//   - Left / Right        → loads the prev / next sibling of the current child
//   - Ctrl+X then Down    → loads the most-recent child of the current session
//                           (chord; the second key must arrive within
//                            HIERARCHY_NAV_CHORD_TIMEOUT_MS or the chord
//                            cancels)
//
// All bindings are silently no-op when focus is inside an editable field
// (input, textarea, contenteditable). This keeps Up/Down/Left/Right
// available for cursor navigation in the message composer.
//
// `installSessionHierarchyNav` returns a teardown that removes the listener
// and clears any pending chord timer.

function dispatchKey(opts: {
  key: string;
  ctrlKey?: boolean;
  metaKey?: boolean;
  target?: EventTarget;
}): KeyboardEvent {
  const event = new KeyboardEvent("keydown", {
    key: opts.key,
    ctrlKey: opts.ctrlKey ?? false,
    metaKey: opts.metaKey ?? false,
    bubbles: true,
    cancelable: true,
  });
  const target = opts.target ?? document.body;
  target.dispatchEvent(event);
  return event;
}

function summary(id: string, parentId: string | undefined, createdAt: string) {
  return {
    id,
    agentId: "agent-x",
    title: id,
    parentId,
    createdAt,
    updatedAt: createdAt,
    messageCount: 0,
    status: "active",
    depth: 0,
    isStreaming: false,
  };
}

describe("useSessionHierarchyNav", () => {
  let teardown: () => void;

  beforeEach(() => {
    setActivePinia(createPinia());
    vi.useFakeTimers();
    teardown = installSessionHierarchyNav();
  });

  afterEach(() => {
    teardown();
    vi.useRealTimers();
  });

  it("Up loads the parent session when the active session is a child", async () => {
    const chatStore = useChatStore();
    chatStore.sessions = [
      summary("parent-1", undefined, "2026-01-01T00:00:00Z"),
      summary("child-a", "parent-1", "2026-01-01T00:01:00Z"),
    ];
    chatStore.currentSessionId = "child-a";
    const loadSpy = vi
      .spyOn(chatStore, "loadSessionMessages")
      .mockResolvedValue();

    dispatchKey({ key: "ArrowUp" });

    expect(loadSpy).toHaveBeenCalledWith("parent-1");
  });

  it("Up is a no-op on a parent session", async () => {
    const chatStore = useChatStore();
    chatStore.sessions = [
      summary("parent-1", undefined, "2026-01-01T00:00:00Z"),
    ];
    chatStore.currentSessionId = "parent-1";
    const loadSpy = vi
      .spyOn(chatStore, "loadSessionMessages")
      .mockResolvedValue();

    dispatchKey({ key: "ArrowUp" });

    expect(loadSpy).not.toHaveBeenCalled();
  });

  it("Left / Right load previous and next siblings on a child session", async () => {
    const chatStore = useChatStore();
    chatStore.sessions = [
      summary("parent-1", undefined, "2026-01-01T00:00:00Z"),
      summary("child-a", "parent-1", "2026-01-01T00:01:00Z"),
      summary("child-b", "parent-1", "2026-01-01T00:02:00Z"),
      summary("child-c", "parent-1", "2026-01-01T00:03:00Z"),
    ];
    chatStore.currentSessionId = "child-b";
    const loadSpy = vi
      .spyOn(chatStore, "loadSessionMessages")
      .mockResolvedValue();

    dispatchKey({ key: "ArrowLeft" });
    expect(loadSpy).toHaveBeenLastCalledWith("child-a");

    chatStore.currentSessionId = "child-b";
    dispatchKey({ key: "ArrowRight" });
    expect(loadSpy).toHaveBeenLastCalledWith("child-c");
  });

  it("Left / Right are a no-op on a parent session", () => {
    const chatStore = useChatStore();
    chatStore.sessions = [
      summary("parent-1", undefined, "2026-01-01T00:00:00Z"),
      summary("child-a", "parent-1", "2026-01-01T00:01:00Z"),
    ];
    chatStore.currentSessionId = "parent-1";
    const loadSpy = vi
      .spyOn(chatStore, "loadSessionMessages")
      .mockResolvedValue();

    dispatchKey({ key: "ArrowLeft" });
    dispatchKey({ key: "ArrowRight" });

    expect(loadSpy).not.toHaveBeenCalled();
  });

  it("Left / Right clamp at the ends (no wrap)", () => {
    const chatStore = useChatStore();
    chatStore.sessions = [
      summary("parent-1", undefined, "2026-01-01T00:00:00Z"),
      summary("child-a", "parent-1", "2026-01-01T00:01:00Z"),
      summary("child-b", "parent-1", "2026-01-01T00:02:00Z"),
    ];
    const loadSpy = vi
      .spyOn(chatStore, "loadSessionMessages")
      .mockResolvedValue();

    chatStore.currentSessionId = "child-a";
    dispatchKey({ key: "ArrowLeft" });
    expect(loadSpy).not.toHaveBeenCalled();

    chatStore.currentSessionId = "child-b";
    dispatchKey({ key: "ArrowRight" });
    expect(loadSpy).not.toHaveBeenCalled();
  });

  it("Ctrl+X then Down loads the most-recent child of the active session", () => {
    const chatStore = useChatStore();
    chatStore.sessions = [
      summary("parent-1", undefined, "2026-01-01T00:00:00Z"),
      summary("child-a", "parent-1", "2026-01-01T00:01:00Z"),
      summary("child-b", "parent-1", "2026-01-01T00:03:00Z"),
      summary("child-c", "parent-1", "2026-01-01T00:02:00Z"),
    ];
    chatStore.currentSessionId = "parent-1";
    const loadSpy = vi
      .spyOn(chatStore, "loadSessionMessages")
      .mockResolvedValue();

    dispatchKey({ key: "x", ctrlKey: true });
    dispatchKey({ key: "ArrowDown" });

    expect(loadSpy).toHaveBeenCalledWith("child-b");
  });

  it("Ctrl+X chord cancels after the configured timeout", () => {
    const chatStore = useChatStore();
    chatStore.sessions = [
      summary("parent-1", undefined, "2026-01-01T00:00:00Z"),
      summary("child-a", "parent-1", "2026-01-01T00:01:00Z"),
    ];
    chatStore.currentSessionId = "parent-1";
    const loadSpy = vi
      .spyOn(chatStore, "loadSessionMessages")
      .mockResolvedValue();

    dispatchKey({ key: "x", ctrlKey: true });
    vi.advanceTimersByTime(HIERARCHY_NAV_CHORD_TIMEOUT_MS + 50);
    dispatchKey({ key: "ArrowDown" });

    expect(loadSpy).not.toHaveBeenCalled();
  });

  it("Ctrl+X chord cancels when an unrelated key arrives between the two", () => {
    const chatStore = useChatStore();
    chatStore.sessions = [
      summary("parent-1", undefined, "2026-01-01T00:00:00Z"),
      summary("child-a", "parent-1", "2026-01-01T00:01:00Z"),
    ];
    chatStore.currentSessionId = "parent-1";
    const loadSpy = vi
      .spyOn(chatStore, "loadSessionMessages")
      .mockResolvedValue();

    dispatchKey({ key: "x", ctrlKey: true });
    dispatchKey({ key: "a" });
    dispatchKey({ key: "ArrowDown" });

    expect(loadSpy).not.toHaveBeenCalled();
  });

  it("does not fire when focus is inside an input element", () => {
    const chatStore = useChatStore();
    chatStore.sessions = [
      summary("parent-1", undefined, "2026-01-01T00:00:00Z"),
      summary("child-a", "parent-1", "2026-01-01T00:01:00Z"),
    ];
    chatStore.currentSessionId = "child-a";
    const loadSpy = vi
      .spyOn(chatStore, "loadSessionMessages")
      .mockResolvedValue();

    const input = document.createElement("input");
    document.body.appendChild(input);
    try {
      dispatchKey({ key: "ArrowUp", target: input });
      dispatchKey({ key: "ArrowLeft", target: input });
      dispatchKey({ key: "ArrowRight", target: input });

      expect(loadSpy).not.toHaveBeenCalled();
    } finally {
      input.remove();
    }
  });

  it("does not fire when focus is inside a textarea element", () => {
    const chatStore = useChatStore();
    chatStore.sessions = [
      summary("parent-1", undefined, "2026-01-01T00:00:00Z"),
      summary("child-a", "parent-1", "2026-01-01T00:01:00Z"),
    ];
    chatStore.currentSessionId = "child-a";
    const loadSpy = vi
      .spyOn(chatStore, "loadSessionMessages")
      .mockResolvedValue();

    const textarea = document.createElement("textarea");
    document.body.appendChild(textarea);
    try {
      dispatchKey({ key: "ArrowUp", target: textarea });

      expect(loadSpy).not.toHaveBeenCalled();
    } finally {
      textarea.remove();
    }
  });

  it("does not fire when focus is inside a contenteditable element", () => {
    const chatStore = useChatStore();
    chatStore.sessions = [
      summary("parent-1", undefined, "2026-01-01T00:00:00Z"),
      summary("child-a", "parent-1", "2026-01-01T00:01:00Z"),
    ];
    chatStore.currentSessionId = "child-a";
    const loadSpy = vi
      .spyOn(chatStore, "loadSessionMessages")
      .mockResolvedValue();

    const editable = document.createElement("div");
    editable.setAttribute("contenteditable", "true");
    document.body.appendChild(editable);
    try {
      dispatchKey({ key: "ArrowUp", target: editable });

      expect(loadSpy).not.toHaveBeenCalled();
    } finally {
      editable.remove();
    }
  });

  it("teardown removes the listener so subsequent keys are ignored", () => {
    const chatStore = useChatStore();
    chatStore.sessions = [
      summary("parent-1", undefined, "2026-01-01T00:00:00Z"),
      summary("child-a", "parent-1", "2026-01-01T00:01:00Z"),
    ];
    chatStore.currentSessionId = "child-a";
    const loadSpy = vi
      .spyOn(chatStore, "loadSessionMessages")
      .mockResolvedValue();

    teardown();
    teardown = () => {}; // prevent double-teardown in afterEach

    dispatchKey({ key: "ArrowUp" });

    expect(loadSpy).not.toHaveBeenCalled();
  });
});
