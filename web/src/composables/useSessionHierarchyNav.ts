import { useChatStore } from "@/stores/chatStore";

// Chord timeout: how long after Ctrl+X we wait for the second key. Exported
// so tests can advance fake timers past the boundary.
export const HIERARCHY_NAV_CHORD_TIMEOUT_MS = 1500;

// Editable focus detection — Up/Down/Left/Right are precious in text fields,
// so we silently no-op when focus is inside an input, textarea, or
// contenteditable element. This keeps cursor and message-list scroll
// behaviour intact.
function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tag = target.tagName;
  if (tag === "INPUT" || tag === "TEXTAREA") return true;
  // jsdom doesn't always reflect isContentEditable through the IDL property,
  // so consult the attribute directly as a fallback.
  if (target.isContentEditable) return true;
  const attr = target.getAttribute("contenteditable");
  if (attr !== null && attr !== "false") return true;
  return false;
}

// installSessionHierarchyNav wires a single document-level keydown listener
// that drives session-hierarchy navigation:
//   - Up                  → parent of current child session
//   - Left / Right        → previous / next sibling
//   - Ctrl+X then Down    → most-recent child of the current session
//
// Returns a teardown function to unbind. Designed to be called from
// ChatView.vue's onMounted / onBeforeUnmount lifecycle, but the function is
// framework-agnostic so it can be tested directly.
export function installSessionHierarchyNav(): () => void {
  let chordArmed = false;
  let chordTimer: ReturnType<typeof setTimeout> | null = null;

  function disarmChord(): void {
    chordArmed = false;
    if (chordTimer !== null) {
      clearTimeout(chordTimer);
      chordTimer = null;
    }
  }

  function armChord(): void {
    chordArmed = true;
    if (chordTimer !== null) clearTimeout(chordTimer);
    chordTimer = setTimeout(() => {
      chordArmed = false;
      chordTimer = null;
    }, HIERARCHY_NAV_CHORD_TIMEOUT_MS);
  }

  function handle(event: KeyboardEvent): void {
    if (isEditableTarget(event.target)) {
      // Don't even consider chord state inside editables — leaves the
      // composer fully responsive.
      return;
    }

    const chatStore = useChatStore();

    // Chord completion: Ctrl+X then Down → load most-recent child.
    if (chordArmed && event.key === "ArrowDown") {
      disarmChord();
      const childId = chatStore.lastDelegatedSessionId;
      if (childId) {
        event.preventDefault();
        void chatStore.loadSessionMessages(childId);
      }
      return;
    }

    // Any non-chord key cancels a pending chord (so Ctrl+X-then-something-else
    // doesn't silently swallow the next ArrowDown).
    if (chordArmed) {
      disarmChord();
    }

    // Chord arming: Ctrl+X (lower or upper, ignore meta/cmd to keep macOS
    // platform-cut still clean).
    if (
      event.ctrlKey &&
      (event.key === "x" || event.key === "X") &&
      !event.metaKey
    ) {
      armChord();
      return;
    }

    if (event.key === "ArrowUp") {
      const parentId = chatStore.parentSessionId;
      if (parentId) {
        event.preventDefault();
        void chatStore.loadSessionMessages(parentId);
      }
      return;
    }

    if (event.key === "ArrowLeft") {
      const prevId = chatStore.previousSiblingSessionId;
      if (prevId) {
        event.preventDefault();
        void chatStore.loadSessionMessages(prevId);
      }
      return;
    }

    if (event.key === "ArrowRight") {
      const nextId = chatStore.nextSiblingSessionId;
      if (nextId) {
        event.preventDefault();
        void chatStore.loadSessionMessages(nextId);
      }
      return;
    }
  }

  document.addEventListener("keydown", handle);

  return () => {
    document.removeEventListener("keydown", handle);
    disarmChord();
  };
}
