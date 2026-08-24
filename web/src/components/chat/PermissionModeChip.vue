<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from "vue";
import {
  DEFAULT_PERMISSION_MODE,
  PERMISSION_MODES,
  useChatStore,
  type PermissionMode,
} from "@/stores/chatStore";

/**
 * PermissionModeChip — composer-toolbar affordance for the session's
 * permission-mode dial. Slice 2 of the Permission Modes (May 2026)
 * plan: visually complete, localStorage-persisted, no backend coupling.
 *
 * Why a chip-plus-popover affordance rather than a select / dropdown:
 *   - The four modes need 1-line descriptions and a risk-tinted
 *     palette to signal severity; a native select cannot render any
 *     of that. A popover gives us a small panel with per-row tooltip
 *     copy and a tick on the active mode.
 *   - The chip mounts in the composer toolbar adjacent to the model
 *     chip ("this turn's policies" grouping per plan §3). The
 *     resting state is a compact pill; the popover is an on-demand
 *     surface.
 *
 * Risk-tint palette (plan §3, extended by ModeAskUser Extension §3):
 *   - Plan         → grey   (read-only, lowest risk)
 *   - Default      → blue   (neutral baseline)
 *   - Accept Edits → amber  (file mutations bypass pathguard)
 *   - Ask          → purple (interactive — operator grants per call,
 *                            distinct from amber relaxed + red bypassed
 *                            so the operator reads "intervenes, not
 *                            loosens")
 *   - YOLO         → red    (all path checks bypassed)
 *
 * Loud disclosure (plan §5):
 *   When the popover is open, a one-line disclosure renders directly
 *   under the Default row stating that Default mode does NOT prompt
 *   per tool call and pointing the operator at the session timeline.
 *   This is the v1 mitigation for the "looser than Claude Code's
 *   Default" gap — the message is rendered in the DOM (not a
 *   hover-only `title`) so screen readers and visual scanning both
 *   pick it up.
 *
 * Slice 3 will replace the localStorage write inside the store with
 * a POST to /api/sessions/{id}/permission-mode and add cross-tab
 * synchronisation. The chip's render contract stays unchanged across
 * slices — the persistence layer is what moves.
 */
defineOptions({ name: "PermissionModeChip" });

interface ModeMeta {
  id: PermissionMode;
  label: string;
  icon: string;
  description: string;
  severity: "neutral" | "info" | "warning" | "ask" | "danger";
}

/**
 * Per-mode metadata. Order is intentional: Plan / Default / Accept Edits
 * / Ask / YOLO. The first four ascend in permissiveness (Plan least,
 * Accept Edits more, YOLO most). Ask is interactive rather than
 * passively-permissive — it sits between Accept Edits and YOLO in the
 * popover order because an operator who already trusts Accept Edits is
 * the natural next reader for "I want to be asked before each
 * unscoped action"; YOLO remains the loudest row at the bottom. The
 * order mirrors the §2 ModeAskUser Extension table.
 */
const MODES: readonly ModeMeta[] = [
  {
    id: "plan",
    label: "Plan",
    icon: "P",
    description: "Read-only. No file edits, no bash mutations.",
    severity: "neutral",
  },
  {
    id: "default",
    label: "Default",
    icon: "D",
    description: "Standard. Pathguard enforced. Mutations logged.",
    severity: "info",
  },
  {
    id: "accept_edits",
    label: "Accept Edits",
    icon: "E",
    description: "Pathguard relaxed for file edits. Bash still enforced.",
    severity: "warning",
  },
  {
    id: "ask",
    label: "Ask",
    icon: "A",
    // ModeAskUser Extension (May 2026) §2 tooltip body — pinned
    // literal copy. The chip's option-description is the §3 surface
    // the operator reads at the moment of choice; the full sentence
    // sits inline (not hover-only) so screen readers + visual
    // scanning both pick up the per-resource persistence semantics.
    description:
      "Pathguard prompts on denial. Operator grants per call. Per-resource grants persist to permissions.yaml.",
    severity: "ask",
  },
  {
    id: "yolo",
    label: "YOLO",
    icon: "Y",
    description: "All path checks bypassed. Allowlist still gates tools.",
    severity: "danger",
  },
];

const chatStore = useChatStore();
const isOpen = ref(false);

const currentMode = computed<PermissionMode>(
  () => chatStore.permissionMode ?? DEFAULT_PERMISSION_MODE,
);

const currentMeta = computed<ModeMeta>(
  () => MODES.find((m) => m.id === currentMode.value) ?? MODES[1],
);

const chipClass = computed(
  () => `permission-mode-chip permission-mode-chip--${currentMeta.value.severity}`,
);

function toggleOpen(): void {
  isOpen.value = !isOpen.value;
}

function close(): void {
  isOpen.value = false;
}

function selectMode(mode: PermissionMode): void {
  chatStore.setPermissionMode(mode);
  close();
}

/**
 * Dismiss the popover on outside click. The handler is attached only
 * while the popover is open so it does not run on every click in the
 * page during the chip's resting state.
 */
function onWindowClick(event: MouseEvent): void {
  if (!isOpen.value) {
    return;
  }
  const target = event.target as HTMLElement | null;
  if (!target) {
    return;
  }
  if (target.closest('[data-testid="permission-mode-chip-root"]')) {
    return;
  }
  close();
}

if (typeof window !== "undefined") {
  window.addEventListener("click", onWindowClick);
}

onBeforeUnmount(() => {
  if (typeof window !== "undefined") {
    window.removeEventListener("click", onWindowClick);
  }
});

// Exposed for the closed-vocabulary assertion in test specs; the
// runtime tuple from the store stays the single source of truth so
// the chip can never drift from the persisted set.
const ALL_MODES = PERMISSION_MODES;
</script>

<template>
  <div
    class="permission-mode-chip-root"
    data-testid="permission-mode-chip-root"
  >
    <button
      type="button"
      :class="chipClass"
      data-testid="permission-mode-chip"
      :data-mode="currentMode"
      :data-severity="currentMeta.severity"
      :aria-expanded="isOpen"
      aria-haspopup="listbox"
      :title="`Permission mode: ${currentMeta.label}`"
      @click.stop="toggleOpen"
    >
      <span class="permission-mode-chip__icon" aria-hidden="true">{{ currentMeta.icon }}</span>
      <span class="permission-mode-chip__label" data-testid="permission-mode-chip-label">
        {{ currentMeta.label }}
      </span>
    </button>

    <div
      v-if="isOpen"
      class="permission-mode-chip__popover"
      data-testid="permission-mode-chip-popover"
      role="listbox"
      aria-label="Permission mode"
    >
      <template v-for="mode in MODES" :key="mode.id">
        <button
          type="button"
          class="permission-mode-chip__option"
          :class="{
            'permission-mode-chip__option--active': mode.id === currentMode,
            [`permission-mode-chip__option--${mode.severity}`]: true,
          }"
          :data-testid="`permission-mode-option-${mode.id}`"
          role="option"
          :aria-selected="mode.id === currentMode"
          @click.stop="selectMode(mode.id)"
        >
          <span class="permission-mode-chip__option-icon" aria-hidden="true">
            {{ mode.icon }}
          </span>
          <span class="permission-mode-chip__option-body">
            <span class="permission-mode-chip__option-label">
              {{ mode.label }}
              <span
                v-if="mode.id === currentMode"
                class="permission-mode-chip__option-tick"
                aria-hidden="true"
              >&check;</span>
            </span>
            <span class="permission-mode-chip__option-description">
              {{ mode.description }}
            </span>
          </span>
        </button>

        <!--
          Loud disclosure (plan §5) — rendered directly under the
          Default row so the operator reads it at the moment of
          choosing the mode. Visible whenever the popover is open
          regardless of which mode is currently active.
          See `feedback_dont_defer_violations_of_stated_intent` —
          this is the in-UI mitigation for v1 Default being looser
          than Claude Code's Default.
        -->
        <p
          v-if="mode.id === 'default'"
          class="permission-mode-chip__disclosure"
          data-testid="permission-mode-default-disclosure"
        >
          Default mode does not prompt per tool call. Review the session timeline for what ran.
        </p>
      </template>
    </div>
    <!-- Constant referenced for type-tooling parity; not rendered. -->
    <span hidden aria-hidden="true" data-testid="permission-mode-chip-vocab">
      {{ ALL_MODES.join(",") }}
    </span>
  </div>
</template>

<style scoped>
.permission-mode-chip-root {
  position: relative;
  display: inline-flex;
  align-items: center;
  flex-shrink: 0;
}

/*
 * Resting chip — visually weighted to read at peripheral vision as a
 * coloured pill in the composer toolbar. Same shape language as
 * ContextUsageChip / QuotaChip so the toolbar reads as a single chip
 * row.
 */
.permission-mode-chip {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  padding: 0.2rem 0.55rem;
  border-radius: var(--radius, 6px);
  border: 1px solid rgba(255, 255, 255, 0.12);
  background: rgba(255, 255, 255, 0.05);
  color: var(--text-primary, #f5f5f5);
  font-size: 0.78rem;
  font-family: var(--font-mono, ui-monospace, monospace);
  cursor: pointer;
  line-height: 1.2;
}

.permission-mode-chip:hover {
  background: rgba(255, 255, 255, 0.09);
}

.permission-mode-chip__icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1rem;
  height: 1rem;
  border-radius: 999px;
  font-size: 0.65rem;
  font-weight: 700;
  background: currentColor;
  color: var(--surface, #1a1a1a);
}

/*
 * Severity palettes ride the theme variables (--warning, --error,
 * --accent) so a theme swap re-skins the chip without touching this
 * stylesheet. Mirrors the idiom established by ContextUsageChip and
 * CriticalErrorBanner.
 */
.permission-mode-chip--neutral {
  border-color: rgba(255, 255, 255, 0.15);
  color: var(--text-muted, #b0b0b0);
}

.permission-mode-chip--info {
  border-color: color-mix(in srgb, var(--accent, #4f9dff) 50%, transparent);
  background: color-mix(in srgb, var(--accent, #4f9dff) 12%, transparent);
  color: var(--accent, #4f9dff);
}

.permission-mode-chip--warning {
  border-color: color-mix(in srgb, var(--warning, #f5a623) 50%, transparent);
  background: color-mix(in srgb, var(--warning, #f5a623) 14%, transparent);
  color: var(--warning, #f5a623);
}

/*
 * Ask palette — purple. ModeAskUser Extension (May 2026) §3 spec:
 * distinct from amber Accept-Edits + red YOLO so the operator reads
 * "interactive, not relaxed". The fallback hex (#a855f7, Tailwind
 * purple-500 family) is overridable by the `--ask` theme variable so
 * a future palette pass can re-skin without touching this component.
 */
.permission-mode-chip--ask {
  border-color: color-mix(in srgb, var(--ask, #a855f7) 50%, transparent);
  background: color-mix(in srgb, var(--ask, #a855f7) 14%, transparent);
  color: var(--ask, #a855f7);
}

.permission-mode-chip--danger {
  border-color: color-mix(in srgb, var(--error, #dc2626) 55%, transparent);
  background: color-mix(in srgb, var(--error, #dc2626) 16%, transparent);
  color: var(--error, #dc2626);
  font-weight: 600;
}

/*
 * Popover — anchored above the chip with bottom-left origin so it
 * never falls off the bottom of the viewport when the composer sits
 * at the bottom of the chat surface.
 */
.permission-mode-chip__popover {
  position: absolute;
  bottom: calc(100% + 6px);
  left: 0;
  z-index: 50;
  min-width: 16rem;
  padding: 0.4rem;
  background: var(--surface, #1a1a1a);
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: var(--radius, 6px);
  box-shadow: 0 6px 24px rgba(0, 0, 0, 0.35);
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
}

.permission-mode-chip__option {
  display: flex;
  align-items: flex-start;
  gap: 0.5rem;
  padding: 0.45rem 0.5rem;
  border-radius: var(--radius, 6px);
  background: transparent;
  border: 1px solid transparent;
  color: var(--text-primary, #f5f5f5);
  cursor: pointer;
  text-align: left;
  font-family: inherit;
  font-size: 0.8rem;
  line-height: 1.3;
}

.permission-mode-chip__option:hover {
  background: rgba(255, 255, 255, 0.06);
}

.permission-mode-chip__option--active {
  background: rgba(255, 255, 255, 0.08);
  border-color: rgba(255, 255, 255, 0.18);
}

.permission-mode-chip__option--neutral .permission-mode-chip__option-icon {
  background: var(--text-muted, #b0b0b0);
  color: var(--surface, #1a1a1a);
}

.permission-mode-chip__option--info .permission-mode-chip__option-icon {
  background: var(--accent, #4f9dff);
  color: var(--surface, #1a1a1a);
}

.permission-mode-chip__option--warning .permission-mode-chip__option-icon {
  background: var(--warning, #f5a623);
  color: var(--surface, #1a1a1a);
}

/*
 * Ask row icon — purple chip on the popover row, matching the chip
 * palette above so the resting chip and the popover option read as
 * the same colour-coded affordance.
 */
.permission-mode-chip__option--ask .permission-mode-chip__option-icon {
  background: var(--ask, #a855f7);
  color: var(--surface, #1a1a1a);
}

.permission-mode-chip__option--danger .permission-mode-chip__option-icon {
  background: var(--error, #dc2626);
  color: var(--surface, #1a1a1a);
}

.permission-mode-chip__option-icon {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.1rem;
  height: 1.1rem;
  border-radius: 999px;
  font-weight: 700;
  font-size: 0.7rem;
  margin-top: 0.05rem;
}

.permission-mode-chip__option-body {
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
}

.permission-mode-chip__option-label {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  font-weight: 600;
}

.permission-mode-chip__option-tick {
  color: var(--accent, #4f9dff);
  font-size: 0.85rem;
  line-height: 1;
}

.permission-mode-chip__option-description {
  color: var(--text-muted, #b0b0b0);
  font-size: 0.72rem;
}

/*
 * Loud-disclosure paragraph — sits between the Default row and the
 * Accept Edits row, slightly indented and toned-down so it reads as
 * a sibling annotation to the Default option rather than a separate
 * mode. The styling is informational, not decorative — pre-attentive
 * weight stays with the option rows themselves.
 */
.permission-mode-chip__disclosure {
  margin: 0.1rem 0.5rem 0.25rem 1.85rem;
  font-size: 0.7rem;
  line-height: 1.35;
  color: var(--text-muted, #b0b0b0);
  font-style: italic;
}
</style>
