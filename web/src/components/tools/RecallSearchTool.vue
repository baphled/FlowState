<script setup lang="ts">
// RecallSearchTool.vue
//
// Renders Recall search-style tool results (search_context,
// chain_search_context, get_messages, chain_get_messages) as a tidy list
// rather than dumping the raw "role: content\n---\n" body the Go side
// returns from internal/recall/query_tools.go::formatMessages.
//
// Body shape: "<role>: <content>\n---\n<role>: <content>" — see
// internal/recall/query_tools.go:170-186 for the canonical formatter and
// internal/tool/recall/chain_search.go:115-119 / chain_messages.go:111-115
// for the chain variants.
//
// We split on the "---" separator, peel the leading "role:" prefix off each
// chunk for display, and cap the visible result count at maxVisible (10) to
// keep the panel compact. Anything beyond that gets a "and N more" hint.
import { computed } from 'vue'
import ToolBubble from './ToolBubble.vue'
import type { ToolRendererProps } from './toolRendererProps'

const maxVisible = 10

interface RecallResult {
  source: string
  snippet: string
}

const props = withDefaults(defineProps<ToolRendererProps>(), {
  status: 'completed',
})

function parseQuery(raw: string | undefined): string | null {
  if (!raw) return null
  try {
    const parsed: unknown = JSON.parse(raw)
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      const args = parsed as Record<string, unknown>
      const query = args.query
      return typeof query === 'string' && query.length > 0 ? query : null
    }
  } catch {
    // Fall through.
  }
  return null
}

function parseResults(body: string): RecallResult[] {
  if (!body || body.trim().length === 0) return []
  return body
    .split(/\n---\n/)
    .map((chunk) => chunk.trim())
    .filter((chunk) => chunk.length > 0)
    .map((chunk) => {
      const colon = chunk.indexOf(':')
      if (colon > 0 && colon < 32) {
        const source = chunk.slice(0, colon).trim()
        const snippet = chunk.slice(colon + 1).trim()
        if (source && snippet) {
          return { source, snippet }
        }
      }
      return { source: 'context', snippet: chunk }
    })
}

const queryText = computed<string | null>(() => {
  const fromInput = parseQuery(props.toolInput)
  if (fromInput) return fromInput
  // search_context falls back to the heading when toolInput is unparseable;
  // get_messages variants have no query at all.
  return props.heading && props.heading !== props.toolName ? props.heading : null
})

const results = computed(() => parseResults(props.body))

const visibleResults = computed(() => results.value.slice(0, maxVisible))

const overflowCount = computed(() => Math.max(0, results.value.length - maxVisible))

const subtitle = computed(() => {
  if (results.value.length === 0) return undefined
  if (overflowCount.value > 0) {
    return `${visibleResults.value.length} of ${results.value.length} results`
  }
  return `${results.value.length} ${results.value.length === 1 ? 'result' : 'results'}`
})
</script>

<template>
  <ToolBubble
    :tool-name="props.toolName"
    :title="props.toolName"
    :subtitle="subtitle"
    :status="props.status"
    :default-open="true"
  >
    <div class="tool-renderer" data-component="recall-search-tool">
      <div v-if="queryText" class="recall-query" data-testid="recall-query">
        <span class="recall-label">Query</span>
        <code class="recall-query-text">{{ queryText }}</code>
      </div>

      <p
        v-if="results.length === 0"
        class="recall-empty"
        data-testid="recall-empty"
      >
        No results.
      </p>

      <ol v-else class="recall-results">
        <li
          v-for="(result, index) in visibleResults"
          :key="`${index}-${result.source}`"
          class="recall-result"
          data-testid="recall-result"
        >
          <span class="recall-source">{{ result.source }}</span>
          <span class="recall-snippet">{{ result.snippet }}</span>
        </li>
      </ol>

      <p
        v-if="overflowCount > 0"
        class="recall-overflow"
        data-testid="recall-overflow"
      >
        and {{ overflowCount }} more
      </p>
    </div>
  </ToolBubble>
</template>

<style scoped>
.tool-renderer {
  display: grid;
  gap: 0.6rem;
}

.recall-query {
  display: flex;
  align-items: baseline;
  gap: 0.6rem;
  font-size: 0.8rem;
}

.recall-label {
  color: var(--text-secondary, #a9b1d6);
  text-transform: uppercase;
  letter-spacing: 0.04em;
  font-size: 0.7rem;
  font-weight: 600;
}

.recall-query-text {
  color: var(--text-primary, #c0caf5);
  background: var(--surface-low, #1a1b26);
  padding: 0.15rem 0.45rem;
  border-radius: calc(var(--radius, 12px) - 6px);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
}

.recall-results {
  list-style: decimal;
  padding-left: 1.2rem;
  margin: 0;
  display: grid;
  gap: 0.4rem;
}

.recall-result {
  display: grid;
  gap: 0.15rem;
  font-size: 0.82rem;
  line-height: 1.5;
}

.recall-source {
  color: var(--text-secondary, #a9b1d6);
  font-weight: 600;
  font-size: 0.7rem;
  text-transform: lowercase;
  letter-spacing: 0.02em;
}

.recall-snippet {
  color: var(--text-primary, #c0caf5);
  white-space: pre-wrap;
  word-break: break-word;
}

.recall-empty,
.recall-overflow {
  margin: 0;
  font-size: 0.78rem;
  font-style: italic;
  color: var(--text-muted, #565f89);
}
</style>
