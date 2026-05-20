import { beforeEach, describe, expect, it, vi, afterEach } from "vitest";
import { dismissToast, showToast, updateToast, useToast } from "./useToast";

describe("useToast", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    const { toasts, dismissAll } = useToast();
    dismissAll();
    while (toasts.value.length > 0) {
      toasts.value.pop();
    }
  });

  it("returns an empty toasts array initially", () => {
    const { toasts } = useToast();

    expect(toasts.value).toEqual([]);
  });

  it("adds a toast when showToast is called with a string", () => {
    const { toasts } = useToast();

    showToast("Hello world");

    expect(toasts.value).toHaveLength(1);
    expect(toasts.value[0].message).toBe("Hello world");
    expect(toasts.value[0].variant).toBe("default");
  });

  it("adds a toast when showToast is called with options", () => {
    const { toasts } = useToast();

    showToast({
      message: "Saved successfully",
      title: "Success",
      variant: "success",
    });

    expect(toasts.value).toHaveLength(1);
    expect(toasts.value[0].message).toBe("Saved successfully");
    expect(toasts.value[0].title).toBe("Success");
    expect(toasts.value[0].variant).toBe("success");
  });

  it("assigns a unique id to each toast", () => {
    const { toasts } = useToast();

    showToast("First");
    showToast("Second");

    expect(toasts.value[0].id).not.toBe(toasts.value[1].id);
  });

  it("uses default duration of 3000ms", () => {
    const { toasts } = useToast();

    showToast("Timed message");

    expect(toasts.value[0].duration).toBe(3000);
  });

  it("respects custom duration when provided", () => {
    const { toasts } = useToast();

    showToast({ message: "Quick", duration: 500 });

    expect(toasts.value[0].duration).toBe(500);
  });

  it("auto-dismisses after duration", () => {
    const { toasts } = useToast();

    showToast({ message: "Gone soon", duration: 1000 });

    expect(toasts.value).toHaveLength(1);

    vi.advanceTimersByTime(1000);

    expect(toasts.value).toHaveLength(0);
  });

  it("does not auto-dismiss when duration is 0", () => {
    const { toasts } = useToast();

    showToast({ message: "Sticky", duration: 0 });

    expect(toasts.value).toHaveLength(1);

    vi.advanceTimersByTime(10000);

    expect(toasts.value).toHaveLength(1);
  });

  it("dismisses toast via removeToast", () => {
    const { toasts, removeToast } = useToast();

    showToast("First");
    showToast("Second");
    const id = toasts.value[0].id;

    removeToast(id);

    expect(toasts.value).toHaveLength(1);
    expect(toasts.value[0].message).toBe("Second");
  });

  it("dismisses toast via dismissToast with id", () => {
    const { toasts } = useToast();

    showToast("First");
    showToast("Second");
    const id = toasts.value[1].id;

    dismissToast(id);

    expect(toasts.value).toHaveLength(1);
    expect(toasts.value[0].message).toBe("First");
  });

  it("dismissAll removes all toasts", () => {
    const { toasts, dismissAll } = useToast();

    showToast("First");
    showToast("Second");
    showToast("Third");

    dismissAll();

    expect(toasts.value).toHaveLength(0);
  });

  it("stores action label and callback when provided", () => {
    const { toasts } = useToast();
    const onClick = vi.fn();

    showToast({
      message: "Undo available",
      action: { label: "Undo", onClick },
    });

    expect(toasts.value[0].action).toBeDefined();
    expect(toasts.value[0].action?.label).toBe("Undo");
    expect(toasts.value[0].action?.onClick).toBe(onClick);
  });

  it("triggers action callback when called", () => {
    const { toasts } = useToast();
    const onClick = vi.fn();

    showToast({
      message: "With action",
      action: { label: "Retry", onClick },
    });

    toasts.value[0].action?.onClick();

    expect(onClick).toHaveBeenCalledOnce();
  });

  it("multiple showToast calls stack toasts", () => {
    const { toasts } = useToast();

    showToast("One");
    showToast("Two");
    showToast("Three");

    expect(toasts.value).toHaveLength(3);
    expect(toasts.value[0].message).toBe("One");
    expect(toasts.value[1].message).toBe("Two");
    expect(toasts.value[2].message).toBe("Three");
  });

  it("loading variant defaults to persistent (duration 0)", () => {
    const { toasts } = useToast();

    showToast({ message: "Loading...", variant: "loading" });

    expect(toasts.value[0].duration).toBe(0);
    expect(toasts.value[0].variant).toBe("loading");
  });

  it("explicit duration overrides loading variant default", () => {
    const { toasts } = useToast();

    showToast({ message: "Loading...", variant: "loading", duration: 5000 });

    expect(toasts.value[0].duration).toBe(5000);
  });

  it("error variant uses default duration", () => {
    const { toasts } = useToast();

    showToast({ message: "Oops", variant: "error" });

    expect(toasts.value[0].variant).toBe("error");
    expect(toasts.value[0].duration).toBe(3000);
  });

  it("clears auto-dismiss timer when toast is manually dismissed", () => {
    const { toasts, removeToast } = useToast();
    const consoleSpy = vi.spyOn(console, "error").mockImplementation(() => {});

    showToast({ message: "Gone soon", duration: 1000 });
    const id = toasts.value[0].id;

    removeToast(id);
    vi.advanceTimersByTime(2000);

    expect(toasts.value).toHaveLength(0);
    consoleSpy.mockRestore();
  });

  describe("showToast returns the new toast id", () => {
    it("returns a numeric id that resolves to the just-pushed toast", () => {
      // Aggregating callers (chatStore tool-activity rolling toast) need
      // the id back so they can drive subsequent updateToast calls. The
      // return value is additive — pre-this-PR callers ignored it, and
      // the void-return contract is preserved at the call site since
      // numbers are still discardable.
      const { toasts } = useToast();
      const id = showToast("First");

      expect(typeof id).toBe("number");
      expect(toasts.value).toHaveLength(1);
      expect(toasts.value[0].id).toBe(id);
    });
  });

  describe("updateToast", () => {
    it("patches the message of a live toast in place", () => {
      // The whole point of the API: same id, same DOM position, new
      // copy. The chatStore aggregator relies on this so a multi-tool
      // burst updates one toast rather than spawning parallel ones.
      const { toasts } = useToast();
      const id = showToast({
        message: "Working",
        variant: "loading",
        duration: 0,
      });

      const ok = updateToast(id, { message: "Working — 3 actions" });

      expect(ok).toBe(true);
      expect(toasts.value).toHaveLength(1);
      expect(toasts.value[0].id).toBe(id);
      expect(toasts.value[0].message).toBe("Working — 3 actions");
    });

    it("returns false when the id is unknown (toast already dismissed)", () => {
      // Caller contract: false means "spawn a fresh toast". Without
      // this signal, an aggregator can't recover from the user
      // closing the toast manually mid-burst.
      const ok = updateToast(99999, { message: "too late" });
      expect(ok).toBe(false);
    });

    it("reschedules the auto-dismiss timer when the patch supplies a new duration", () => {
      // Patching duration cancels and re-arms the timer with the new
      // value, anchored at the patch moment. A 0-duration patch makes
      // the toast persistent. Used by aggregators that switch a
      // transient toast into "I own dismissal" mode mid-flight.
      const { toasts } = useToast();
      const id = showToast({ message: "Soon", duration: 1000 });

      // 800ms in — about to auto-dismiss.
      vi.advanceTimersByTime(800);
      expect(toasts.value).toHaveLength(1);

      // Patch to 0 — persistent. The original 1000ms timer should be
      // cancelled, so advancing past it does NOT auto-dismiss.
      updateToast(id, { duration: 0 });
      vi.advanceTimersByTime(1000);
      expect(toasts.value).toHaveLength(1);
    });

    it("patches title, variant, and action without resurrecting cleared fields", () => {
      const { toasts } = useToast();
      const id = showToast({
        message: "Hello",
        title: "Old",
        variant: "default",
      });

      const onClick = vi.fn();
      updateToast(id, {
        title: "New",
        variant: "error",
        action: { label: "Retry", onClick },
      });

      expect(toasts.value[0].title).toBe("New");
      expect(toasts.value[0].variant).toBe("error");
      expect(toasts.value[0].action?.label).toBe("Retry");
    });

    it("leaves untouched fields alone (sparse patches)", () => {
      const { toasts } = useToast();
      const id = showToast({
        message: "Original",
        title: "Hello",
        variant: "success",
      });

      updateToast(id, { message: "Updated" });

      expect(toasts.value[0].message).toBe("Updated");
      expect(toasts.value[0].title).toBe("Hello");
      expect(toasts.value[0].variant).toBe("success");
    });
  });
});
