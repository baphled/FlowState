<template>
  <div class="settings-view" data-testid="settings-view">
    <h1>Settings</h1>

    <section class="settings-section" data-testid="theme-section">
      <h2>Theme</h2>
      <!--
        N1 (Vue UI Parity vs OpenCode, May 2026 UI Parity PR4) — live
        preview on hover. Pointer-enter on a theme option toggles
        <html data-theme> to that option so the user sees the full
        chrome re-skinning in place. Pointer-leave reverts to the
        committed selection. The original selection is the source of
        truth — hover never persists.
      -->
      <div class="theme-options">
        <label
          v-for="option in themeOptions"
          :key="option.value"
          class="theme-option"
          :class="{ active: settingsStore.theme === option.value }"
          :data-testid="`theme-option-${option.value}`"
          @mouseenter="previewTheme(option.value)"
          @mouseleave="endPreview()"
        >
          <input
            type="radio"
            name="theme"
            :value="option.value"
            :checked="settingsStore.theme === option.value"
            @change="settingsStore.setTheme(option.value as Theme)"
          />
          <span class="theme-label">{{ option.label }}</span>
          <span class="theme-preview" :data-theme="option.value" />
        </label>
      </div>
    </section>

    <section class="settings-section" data-testid="api-section">
      <h2>API</h2>
      <label class="field-label" for="api-host">API Host</label>
      <input
        id="api-host"
        class="field-input"
        data-testid="api-host-input"
        type="text"
        :value="settingsStore.apiHost"
        @input="settingsStore.setApiHost(($event.target as HTMLInputElement).value)"
        placeholder="http://localhost:8080"
      />
      <p class="field-hint">
        Base URL for the FlowState Go server. Vite proxies <code>/api</code> in dev mode.
      </p>
    </section>

    <section class="settings-section" data-testid="layout-section">
      <h2>Layout</h2>
      <label class="toggle-label">
        <input
          type="checkbox"
          data-testid="swarm-pane-toggle"
          :checked="settingsStore.swarmPaneVisible"
          @change="settingsStore.toggleSwarmPane()"
        />
        Show swarm activity pane in chat
      </label>
    </section>

    <!--
      Deliverable 2 of the May 2026 context-accuracy bundle —
      runtime-tunable auto-compaction threshold. The section only
      renders when the backend reports a configured threshold (i.e.
      a CompactionController is wired); a 501 from
      /api/v1/config/compression resolves to null and hides the
      control entirely so operators don't see a slider that won't
      function.
    -->
    <section
      v-if="compressionConfig !== null"
      class="settings-section"
      data-testid="compression-section"
    >
      <h2>Context Compression</h2>
      <label class="field-label" for="compression-threshold">
        Auto-compaction threshold ({{ thresholdPercent }}%)
      </label>
      <input
        id="compression-threshold"
        class="field-input"
        data-testid="compression-threshold-input"
        type="range"
        min="0.1"
        max="1.0"
        step="0.05"
        :value="compressionConfig.threshold"
        @change="onThresholdChange(($event.target as HTMLInputElement).value)"
      />
      <p class="field-hint">
        Fire L2 auto-compaction when the persisted context window crosses this
        fraction of the model's context limit. Lower values compact sooner
        (more aggressive); higher values compact later (more headroom for
        long sessions before summary). 0.75 is the historical default.
      </p>
      <p v-if="thresholdError" class="field-error" data-testid="compression-threshold-error">
        {{ thresholdError }}
      </p>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useSettingsStore } from '@/stores/settingsStore'
import {
  fetchCompressionConfig,
  updateCompressionThreshold,
  type CompressionConfig,
} from '@/api'
import type { Theme } from '@/types'

const settingsStore = useSettingsStore()

// N2 (May 2026 UI Parity PR4) — additional community palettes. The
// label values are the human-readable names; the `value` MUST match
// the corresponding `[data-theme="X"]` block in `themes.css` AND the
// Shiki theme key in `markdownHighlighter.ts`.
const themeOptions: { value: Theme; label: string }[] = [
  { value: 'dark', label: 'Dark' },
  { value: 'light', label: 'Light' },
  { value: 'terminal', label: 'Terminal' },
  { value: 'tokyo-night', label: 'Tokyo Night' },
  { value: 'catppuccin-mocha', label: 'Catppuccin Mocha' },
  { value: 'dracula', label: 'Dracula' },
  { value: 'nord', label: 'Nord' },
]

// N1 — live preview helpers. The preview mutates <html data-theme>
// directly (not through the settings store) so leaving the option
// reverts to the persisted selection without touching localStorage.
// `previewTheme` is fire-and-forget — there is no async work and no
// race with `setTheme`.
function previewTheme(value: Theme): void {
  if (typeof document === 'undefined') return
  document.documentElement.setAttribute('data-theme', value)
}

function endPreview(): void {
  if (typeof document === 'undefined') return
  document.documentElement.setAttribute('data-theme', settingsStore.theme)
}

// Deliverable 2 — compression threshold state. `null` means
// "not yet fetched" OR "backend reported 501". The section's v-if
// hides the control in both states; the hydrate-on-mount swap to a
// concrete CompressionConfig is what triggers the render.
const compressionConfig = ref<CompressionConfig | null>(null)
const thresholdError = ref<string>('')

const thresholdPercent = computed(() => {
  const cfg = compressionConfig.value
  if (cfg === null) return 0
  return Math.round(cfg.threshold * 100)
})

onMounted(async () => {
  try {
    compressionConfig.value = await fetchCompressionConfig()
  } catch {
    // Defensive — a network failure on initial fetch hides the
    // control rather than leaving it in a degraded state. Operators
    // can refresh the page once the backend is back up.
    compressionConfig.value = null
  }
})

async function onThresholdChange(rawValue: string): Promise<void> {
  const parsed = parseFloat(rawValue)
  if (Number.isNaN(parsed)) {
    thresholdError.value = 'Threshold must be a number.'
    return
  }
  thresholdError.value = ''
  try {
    const updated = await updateCompressionThreshold(parsed)
    compressionConfig.value = updated
  } catch (err) {
    thresholdError.value = err instanceof Error ? err.message : 'Update failed.'
  }
}
</script>

<style scoped>
.settings-view {
  padding: 1.5rem;
  max-width: 640px;
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  gap: 2rem;
}

h1 {
  font-size: 1.35rem;
  font-weight: 600;
  color: var(--text-primary);
}

.settings-section {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.settings-section h2 {
  font-size: 0.9rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-secondary);
  border-bottom: 1px solid var(--border);
  padding-bottom: 0.4rem;
}

.theme-options {
  display: flex;
  gap: 0.75rem;
}

.theme-option {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.4rem;
  padding: 0.75rem 1.25rem;
  border: 2px solid var(--border);
  border-radius: var(--radius);
  cursor: pointer;
  transition: border-color 0.15s;
}

.theme-option.active {
  border-color: var(--accent);
}

.theme-option input[type='radio'] {
  display: none;
}

.theme-label {
  font-size: 0.85rem;
  color: var(--text-primary);
}

.theme-preview {
  width: 48px;
  height: 28px;
  border-radius: 4px;
  border: 1px solid var(--border);
}

.theme-preview[data-theme='dark'] {
  background: #1a1b26;
}

.theme-preview[data-theme='light'] {
  background: #f8f8f8;
}

.theme-preview[data-theme='terminal'] {
  background: #0d0d0d;
}

/* N2 — community palettes. Swatch is a single colour because the
   preview chip is 48×28; pick the theme's primary background. */
.theme-preview[data-theme='tokyo-night'] {
  background: #1a1b26;
}

.theme-preview[data-theme='catppuccin-mocha'] {
  background: #1e1e2e;
}

.theme-preview[data-theme='dracula'] {
  background: #282a36;
}

.theme-preview[data-theme='nord'] {
  background: #2e3440;
}

.field-label {
  font-size: 0.9rem;
  font-weight: 500;
  color: var(--text-primary);
}

.field-input {
  padding: 0.5rem 0.75rem;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--bg-secondary);
  color: var(--text-primary);
  font-size: 0.9rem;
  width: 100%;
}

.field-hint {
  font-size: 0.8rem;
  color: var(--text-secondary);
}

.field-error {
  font-size: 0.8rem;
  color: var(--error, #dc2626);
}

.toggle-label {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.9rem;
  color: var(--text-primary);
  cursor: pointer;
}
</style>
