import { onBeforeUnmount, ref, watch, type Ref } from "vue";

/**
 * useFocusTrap — keyboard accessibility helper for modal-like overlays.
 *
 * Why: pre-this-PR Tab/Shift+Tab inside FuzzySearchModal, AgentPicker, or
 * ModelPicker would walk the focus into the underlying chat thread,
 * collapsing the modal's keyboard-only contract. Mouse-only users were
 * fine; keyboard-only users were locked out of the rest of the modal's
 * content because the only focusable element they could reach was the
 * search input.
 *
 * Contract:
 *   - When `active` flips true, on the next tick:
 *       * remembers the currently-focused element so we can restore on
 *         deactivation,
 *       * focuses the FIRST focusable child of the container (or the
 *         container itself if it has tabindex),
 *       * installs a window-level keydown listener that traps Tab and
 *         Shift+Tab inside the container.
 *   - When `active` flips false, restores focus to the remembered element
 *     and removes the listener.
 *   - Escape is NOT swallowed — modal owners typically already have an
 *     Escape handler. The trap stays out of the way.
 *
 * The trap is intentionally minimal — no autofocus stealing, no
 * inert-attribute games on siblings (which break Vue Teleport in some
 * Pinia setups). Just Tab cycling within the container.
 */
export interface FocusTrapOptions {
  /**
   * Element from which previously-focused focus should be restored on
   * deactivation. Defaults to `document.activeElement` at activation
   * time. Pass an explicit element when the previously-focused element
   * is known to disappear (e.g. a tear-down trigger button).
   */
  restoreFocusTo?: () => HTMLElement | null;
}

const FOCUSABLE_SELECTORS = [
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  '[tabindex]:not([tabindex="-1"])',
].join(",");

function getFocusableElements(container: HTMLElement): HTMLElement[] {
  return Array.from(
    container.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTORS),
  ).filter((el) => !el.hasAttribute("disabled") && el.offsetParent !== null);
}

export function useFocusTrap(
  containerRef: Ref<HTMLElement | null>,
  active: Ref<boolean>,
  options: FocusTrapOptions = {},
): void {
  const previouslyFocused = ref<HTMLElement | null>(null);

  function onKeydown(event: KeyboardEvent): void {
    if (event.key !== "Tab") return;
    const container = containerRef.value;
    if (!container) return;
    const focusables = getFocusableElements(container);
    if (focusables.length === 0) {
      // Nothing to trap into — keep the focus on whatever the modal
      // owner put there. Stop propagation so the chat thread doesn't
      // steal focus on Tab.
      event.preventDefault();
      return;
    }

    const first = focusables[0];
    const last = focusables[focusables.length - 1];
    const activeEl = document.activeElement as HTMLElement | null;

    if (event.shiftKey) {
      if (activeEl === first || !container.contains(activeEl)) {
        event.preventDefault();
        last.focus();
      }
    } else {
      if (activeEl === last) {
        event.preventDefault();
        first.focus();
      }
    }
  }

  function activate(): void {
    previouslyFocused.value = document.activeElement as HTMLElement | null;
    document.addEventListener("keydown", onKeydown);
    // Focus first focusable on next paint so any container child that
    // appears in the same activation tick (e.g. v-if=open) is in the DOM.
    requestAnimationFrame(() => {
      const container = containerRef.value;
      if (!container) return;
      const focusables = getFocusableElements(container);
      const target = focusables[0] ?? container;
      if (target && typeof target.focus === "function") {
        target.focus();
      }
    });
  }

  function deactivate(): void {
    document.removeEventListener("keydown", onKeydown);
    const restoreTo = options.restoreFocusTo?.() ?? previouslyFocused.value;
    if (
      restoreTo &&
      typeof restoreTo.focus === "function" &&
      restoreTo.isConnected
    ) {
      restoreTo.focus();
    }
    previouslyFocused.value = null;
  }

  watch(active, (isActive, wasActive) => {
    if (isActive && !wasActive) {
      activate();
    } else if (!isActive && wasActive) {
      deactivate();
    }
  });

  onBeforeUnmount(() => {
    if (active.value) {
      deactivate();
    }
  });
}
