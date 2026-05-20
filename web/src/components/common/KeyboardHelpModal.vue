<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, toRef } from "vue";
import { useFocusTrap } from "@/composables/useFocusTrap";
import Icon from "@/components/common/Icon.vue";

defineOptions({ name: "KeyboardHelpModal" });

/**
 * UI Parity PR2 I2 (May 2026) — Keyboard shortcuts help modal.
 *
 * Surfaces the bindings the codebase already implements so power users
 * can discover them without reading the README. Triggered from
 * ChatView's global keydown listener on:
 *   - `?` when no input is focused
 *   - `Ctrl+/` (works everywhere)
 *
 * Closes on Escape, on a click outside the panel, and on the X button.
 * Focus is trapped inside the modal while open so keyboard-only users
 * cannot fall through to the underlying chat thread.
 */
const props = defineProps<{
  open: boolean;
}>();

const emit = defineEmits<{
  close: [];
}>();

const modalEl = ref<HTMLElement | null>(null);

useFocusTrap(modalEl, toRef(props, "open"));

interface ShortcutRow {
  keys: string[];
  description: string;
}

interface ShortcutGroup {
  title: string;
  rows: ShortcutRow[];
}

// Mirrors the bindings actually wired in the app. When a new binding
// lands the author MUST add a row here — the modal is the single source
// of truth for "what shortcuts exist".
const GROUPS: ShortcutGroup[] = [
  {
    title: "Composer",
    rows: [
      { keys: ["Enter"], description: "Send message" },
      { keys: ["Shift", "Enter"], description: "Insert newline" },
      { keys: ["Alt", "Enter"], description: "Insert newline (alt)" },
      {
        keys: ["ArrowUp"],
        description: "Recall previous prompt (when empty / caret at start)",
      },
      { keys: ["ArrowDown"], description: "Walk forward through history" },
      { keys: ["/"], description: "Open slash-command picker" },
      { keys: ["@"], description: "Open agent / swarm mention picker" },
    ],
  },
  {
    title: "Streaming control",
    rows: [
      {
        keys: ["Esc", "Esc"],
        description: "Cancel in-flight turn (press twice within 600ms)",
      },
      { keys: ["Esc"], description: "Close open picker without losing buffer" },
    ],
  },
  {
    title: "Session navigation",
    rows: [
      {
        keys: ["ArrowUp"],
        description: "Go to parent session (when not focused on input)",
      },
      { keys: ["ArrowLeft"], description: "Previous sibling session" },
      { keys: ["ArrowRight"], description: "Next sibling session" },
      {
        keys: ["Ctrl", "X", "→", "ArrowDown"],
        description: "Jump to most-recent child session",
      },
    ],
  },
  {
    title: "Help",
    rows: [
      {
        keys: ["?"],
        description: "Open this shortcut list (when no input focused)",
      },
      {
        keys: ["Ctrl", "/"],
        description: "Open this shortcut list (anywhere)",
      },
    ],
  },
];

function handleEscape(event: KeyboardEvent): void {
  if (event.key === "Escape" && props.open) {
    event.preventDefault();
    event.stopPropagation();
    emit("close");
  }
}

function handleBackdropClick(): void {
  emit("close");
}

function handleCloseButton(): void {
  emit("close");
}

onMounted(() => {
  document.addEventListener("keydown", handleEscape, true);
});

onBeforeUnmount(() => {
  document.removeEventListener("keydown", handleEscape, true);
});
</script>

<template>
  <div
    v-if="open"
    class="keyboard-help-overlay"
    data-testid="keyboard-help-modal"
    role="dialog"
    aria-modal="true"
    aria-labelledby="keyboard-help-title"
  >
    <div
      class="keyboard-help-backdrop"
      data-testid="keyboard-help-backdrop"
      @click="handleBackdropClick"
    />
    <div ref="modalEl" class="keyboard-help-panel">
      <div class="keyboard-help-header">
        <h2 id="keyboard-help-title">Keyboard shortcuts</h2>
        <button
          type="button"
          class="keyboard-help-close"
          data-testid="keyboard-help-close"
          aria-label="Close keyboard shortcuts"
          @click="handleCloseButton"
        >
          <Icon name="close" :size="18" />
        </button>
      </div>

      <div class="keyboard-help-body">
        <section
          v-for="group in GROUPS"
          :key="group.title"
          class="keyboard-help-group"
          :data-testid="`keyboard-help-group-${group.title.toLowerCase().replace(/\s+/g, '-')}`"
        >
          <h3 class="keyboard-help-group-title">{{ group.title }}</h3>
          <dl class="keyboard-help-rows">
            <template
              v-for="(row, idx) in group.rows"
              :key="`${group.title}-${idx}`"
            >
              <dt class="keyboard-help-keys">
                <template v-for="(key, k) in row.keys" :key="k">
                  <kbd class="keyboard-help-kbd">{{ key }}</kbd>
                  <span
                    v-if="k < row.keys.length - 1"
                    class="keyboard-help-plus"
                    >+</span
                  >
                </template>
              </dt>
              <dd class="keyboard-help-description">{{ row.description }}</dd>
            </template>
          </dl>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
.keyboard-help-overlay {
  position: fixed;
  inset: 0;
  z-index: 1100;
  display: flex;
  align-items: center;
  justify-content: center;
}

.keyboard-help-backdrop {
  position: absolute;
  inset: 0;
  background: rgba(0, 0, 0, 0.6);
  backdrop-filter: blur(2px);
}

.keyboard-help-panel {
  position: relative;
  width: 90%;
  max-width: 640px;
  max-height: 80vh;
  background: var(--bg-primary);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg, 8px);
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.4);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.keyboard-help-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.9rem 1.2rem;
  border-bottom: 1px solid var(--border);
  background: var(--bg-elevated);
}

.keyboard-help-header h2 {
  margin: 0;
  font-size: 1.05rem;
  font-weight: 600;
  color: var(--text-primary);
}

.keyboard-help-close {
  background: transparent;
  border: none;
  color: var(--text-muted);
  cursor: pointer;
  padding: 0.25rem;
  border-radius: var(--radius);
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.keyboard-help-close:hover {
  background: var(--bg-secondary);
  color: var(--text-primary);
}

.keyboard-help-body {
  flex: 1;
  overflow-y: auto;
  padding: 1rem 1.2rem;
}

.keyboard-help-group + .keyboard-help-group {
  margin-top: 1.25rem;
}

.keyboard-help-group-title {
  font-size: 0.75rem;
  font-weight: 700;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--text-muted);
  margin: 0 0 0.4rem;
}

.keyboard-help-rows {
  display: grid;
  grid-template-columns: minmax(140px, max-content) 1fr;
  gap: 0.4rem 1rem;
  margin: 0;
}

.keyboard-help-keys {
  display: inline-flex;
  flex-wrap: wrap;
  gap: 0.25rem;
  align-items: center;
}

.keyboard-help-kbd {
  display: inline-block;
  padding: 0.05rem 0.4rem;
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-bottom-width: 2px;
  border-radius: var(--radius);
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 0.78rem;
  color: var(--text-primary);
}

.keyboard-help-plus {
  font-size: 0.7rem;
  color: var(--text-muted);
}

.keyboard-help-description {
  font-size: 0.85rem;
  color: var(--text-primary);
  margin: 0;
}
</style>
