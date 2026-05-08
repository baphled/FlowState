<script lang="ts">
// Render-cap constants. Hard-cap the visible slice to keep <pre> layout
// cheap on the main thread; users can opt in to the full body via the
// "Show full output" toggle. The cap is purely visual — the agent-side
// truncation in internal/tool/truncate already enforces the contract
// the model sees.
export const RENDER_MAX_LINES = 200
export const RENDER_MAX_BYTES = 8 * 1024
</script>

<script setup lang="ts">
import { ref, computed } from 'vue'
import CopyButton from './CopyButton.vue'
import ToolBubble from './ToolBubble.vue'
import type { ToolRendererProps } from './toolRendererProps'

const props = withDefaults(defineProps<ToolRendererProps>(), {
  status: 'completed',
})

const showFull = ref(false)

interface SliceResult {
  body: string
  hiddenLines: number
}

function sliceForRender(body: string): SliceResult {
  // Byte cap first: take up to RENDER_MAX_BYTES, then trim back to a
  // line boundary so the <pre> never shows a half-line. Then enforce
  // the line cap on whatever survived. Whichever cap bites first wins.
  let slice = body.length > RENDER_MAX_BYTES ? body.slice(0, RENDER_MAX_BYTES) : body
  if (slice.length < body.length) {
    const lastNewline = slice.lastIndexOf('\n')
    if (lastNewline > 0) {
      slice = slice.slice(0, lastNewline)
    }
  }

  const lines = slice.split('\n')
  if (lines.length > RENDER_MAX_LINES) {
    slice = lines.slice(0, RENDER_MAX_LINES).join('\n')
  }

  const totalLines = body.split('\n').length
  const renderedLines = slice.split('\n').length
  const hiddenLines = Math.max(0, totalLines - renderedLines)
  return { body: slice, hiddenLines }
}

const renderSlice = computed(() => sliceForRender(props.body))

const bodyTruncated = computed(() => renderSlice.value.hiddenLines > 0)

const displayedBody = computed(() => {
  if (showFull.value || !bodyTruncated.value) {
    return props.body
  }
  return renderSlice.value.body
})

const toggleLabel = computed(() => (showFull.value ? 'Show less' : 'Show full output'))
const toggleAriaLabel = computed(() => (showFull.value ? 'Hide full output' : 'Show full output'))

function toggle() {
  showFull.value = !showFull.value
}
</script>

<template>
  <ToolBubble
    :tool-name="props.toolName"
    :title="props.toolName"
    :status="props.status"
    :default-open="true"
  >
    <div class="tool-renderer" data-component="bash-tool">
      <section class="tool-section">
        <div class="tool-section__header">
          <span class="tool-section__label">Command</span>
          <CopyButton :text="props.heading" />
        </div>
        <pre class="tool-code tool-code--bash" data-component="bash-command"><code>{{ props.heading }}</code></pre>
      </section>

      <section v-if="props.body" class="tool-section">
        <div class="tool-section__header">
          <span class="tool-section__label">Output</span>
          <CopyButton :text="props.body" />
        </div>
        <pre class="tool-code tool-code--output" data-component="bash-output"><code>{{ displayedBody }}</code></pre>
        <p
          v-if="bodyTruncated && !showFull"
          class="bash-tool-truncation-hint"
          data-component="bash-output-truncation-hint"
        >
          {{ renderSlice.hiddenLines }} lines hidden — click "Show full output" to view all.
        </p>
        <button
          v-if="bodyTruncated"
          type="button"
          class="bash-tool-toggle"
          data-component="bash-output-toggle"
          :aria-label="toggleAriaLabel"
          :aria-expanded="showFull ? 'true' : 'false'"
          @click="toggle"
        >
          {{ toggleLabel }}
        </button>
      </section>
    </div>
  </ToolBubble>
</template>

<style scoped>
.tool-renderer {
  display: grid;
  gap: 0.85rem;
}

.tool-section {
  display: grid;
  gap: 0.45rem;
}

.tool-section__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
}

.tool-section__label {
  color: var(--text-secondary, #a9b1d6);
  font-size: 0.78rem;
  font-weight: 600;
  letter-spacing: 0.02em;
  text-transform: uppercase;
}

.tool-code {
  margin: 0;
  padding: 0.85rem 1rem;
  border: 1px solid var(--border, rgba(148, 163, 184, 0.25));
  border-radius: calc(var(--radius, 12px) - 4px);
  background: var(--surface-low, #1a1b26);
  color: var(--text-primary, #c0caf5);
  font-family: ui-monospace, SFMono-Regular, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
  font-size: 0.85rem;
  line-height: 1.5;
  overflow-x: auto;
  white-space: pre-wrap;
  word-break: break-word;
}

.tool-code--output {
  background: var(--surface-hover, #16161e);
}

.bash-tool-truncation-hint {
  margin: 0;
  color: var(--text-secondary, #a9b1d6);
  font-size: 0.78rem;
  font-style: italic;
}

.bash-tool-toggle {
  align-self: flex-start;
  padding: 0.3rem 0.65rem;
  border: 1px solid var(--border, rgba(148, 163, 184, 0.25));
  border-radius: calc(var(--radius, 12px) - 6px);
  background: var(--surface-low, #1a1b26);
  color: var(--text-secondary, #a9b1d6);
  font-size: 0.78rem;
  font-weight: 600;
  letter-spacing: 0.02em;
  text-transform: uppercase;
  cursor: pointer;
}

.bash-tool-toggle:hover,
.bash-tool-toggle:focus-visible {
  color: var(--text-primary, #c0caf5);
  border-color: var(--text-secondary, #a9b1d6);
  outline: none;
}
</style>
