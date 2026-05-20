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
import { computed } from "vue";
import ToolBubble from "./ToolBubble.vue";
import type { ToolRendererProps } from "./toolRendererProps";

const maxVisible = 10;

interface RecallResult {
  source: string;
  snippet: string;
  // UI Parity PR6 N6 — optional provenance carried by chain-search results.
  // The backend formatter (internal/recall/query_tools.go) does NOT yet
  // emit these; this is the UI-side scaffold pinning the wire shape so the
  // backend lift can land without touching the Vue tree. The chunk format
  // when present is:
  //   `[time=<iso>] [depth=<n>] <role>: <snippet>`
  // Either prefix is independent; both are optional.
  timestamp?: string;
  chainDepth?: number;
}

const props = withDefaults(defineProps<ToolRendererProps>(), {
  status: "completed",
});

function parseQuery(raw: string | undefined): string | null {
  if (!raw) return null;
  try {
    const parsed: unknown = JSON.parse(raw);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      const args = parsed as Record<string, unknown>;
      const query = args.query;
      return typeof query === "string" && query.length > 0 ? query : null;
    }
  } catch {
    // Fall through.
  }
  return null;
}

// UI Parity PR6 N6 — strip optional `[time=...]` and `[depth=...]` prefixes
// from a result chunk so the snippet itself is free of metadata. Returns the
// extracted metadata plus the remaining body for the role-colon parser to
// consume.
function extractMetadata(chunk: string): {
  timestamp?: string;
  chainDepth?: number;
  rest: string;
} {
  let rest = chunk;
  let timestamp: string | undefined;
  let chainDepth: number | undefined;
  // Match one prefix at a time so order does not matter and so the parser
  // is tolerant of either-or-both. The regex anchors to the start, with a
  // single token before the trailing `] `.
  for (;;) {
    const m = rest.match(/^\[(time|depth)=([^\]]+)\]\s*/);
    if (!m) break;
    const key = m[1];
    const value = m[2];
    if (key === "time") {
      timestamp = value;
    } else if (key === "depth") {
      const n = Number.parseInt(value, 10);
      if (Number.isFinite(n) && n >= 0) chainDepth = n;
    }
    rest = rest.slice(m[0].length);
  }
  return { timestamp, chainDepth, rest };
}

function parseResults(body: string): RecallResult[] {
  if (!body || body.trim().length === 0) return [];
  return body
    .split(/\n---\n/)
    .map((chunk) => chunk.trim())
    .filter((chunk) => chunk.length > 0)
    .map((chunk) => {
      const { timestamp, chainDepth, rest } = extractMetadata(chunk);
      const stripped = rest.trim();
      const colon = stripped.indexOf(":");
      if (colon > 0 && colon < 32) {
        const source = stripped.slice(0, colon).trim();
        const snippet = stripped.slice(colon + 1).trim();
        if (source && snippet) {
          return { source, snippet, timestamp, chainDepth };
        }
      }
      return { source: "context", snippet: stripped, timestamp, chainDepth };
    });
}

// UI Parity PR6 N6 — relative-time formatter. Mirrors SessionBrowser's
// formatRelativeTime contract ("just now", "Nm ago", "Nh ago", "Nd ago")
// so the recall surface speaks the same vocabulary as the session list.
function formatRelativeTime(iso: string): string {
  const parsed = new Date(iso).getTime();
  if (!Number.isFinite(parsed)) return iso;
  const seconds = Math.floor((Date.now() - parsed) / 1000);
  if (seconds < 60) return "just now";
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  if (seconds < 604800) return `${Math.floor(seconds / 86400)}d ago`;
  return new Date(iso).toLocaleDateString();
}

const queryText = computed<string | null>(() => {
  const fromInput = parseQuery(props.toolInput);
  if (fromInput) return fromInput;
  // search_context falls back to the heading when toolInput is unparseable;
  // get_messages variants have no query at all.
  return props.heading && props.heading !== props.toolName
    ? props.heading
    : null;
});

const results = computed(() => parseResults(props.body));

const visibleResults = computed(() => results.value.slice(0, maxVisible));

const overflowCount = computed(() =>
  Math.max(0, results.value.length - maxVisible),
);

const subtitle = computed(() => {
  if (results.value.length === 0) return undefined;
  if (overflowCount.value > 0) {
    return `${visibleResults.value.length} of ${results.value.length} results`;
  }
  return `${results.value.length} ${results.value.length === 1 ? "result" : "results"}`;
});

// UI Parity I4 (May 2026): recall searches return long result lists.
// Start collapsed; subtitle already shows result count. Force open on
// error so failure cause is visible.
const cardDefaultOpen = computed(() => props.status === "error");
</script>

<template>
  <ToolBubble
    :tool-name="props.toolName"
    :title="props.toolName"
    :subtitle="subtitle"
    :status="props.status"
    :default-open="cardDefaultOpen"
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
          <span class="recall-result-header">
            <span class="recall-source">{{ result.source }}</span>
            <!--
              UI Parity PR6 N6 — optional provenance: relative timestamp +
              chain-hop indicator. Both render only when the chunk carried
              the corresponding metadata prefix, so the current backend
              format (plain `role: content`) is untouched.
            -->
            <span
              v-if="result.timestamp"
              class="recall-timestamp"
              data-testid="recall-timestamp"
              :title="result.timestamp"
              >{{ formatRelativeTime(result.timestamp) }}</span
            >
            <span
              v-if="result.chainDepth !== undefined"
              class="recall-chain-depth"
              data-testid="recall-chain-depth"
              :title="`Chain depth: ${result.chainDepth}`"
              >↑{{ result.chainDepth }}</span
            >
          </span>
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
  font-family:
    ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono",
    "Courier New", monospace;
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

.recall-result-header {
  display: flex;
  align-items: baseline;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.recall-source {
  color: var(--text-secondary, #a9b1d6);
  font-weight: 600;
  font-size: 0.7rem;
  text-transform: lowercase;
  letter-spacing: 0.02em;
}

/*
 * UI Parity PR6 N6 — relative-time + chain-hop badges. Both render in muted
 * tone to keep the snippet itself dominant; the chain depth carries an
 * arrow glyph (↑) so it reads as "this came from N hops back".
 */
.recall-timestamp,
.recall-chain-depth {
  font-size: 0.7rem;
  color: var(--text-muted, #565f89);
  font-weight: 500;
}

.recall-chain-depth {
  color: var(--accent, #7aa2f7);
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
