<script setup lang="ts">
import { computed } from "vue";
import CopyButton from "./CopyButton.vue";
import ToolBubble from "./ToolBubble.vue";
import type { ToolRendererProps } from "./toolRendererProps";

type EditLineKind = "added" | "removed" | "plain";

interface EditLine {
  text: string;
  kind: EditLineKind;
}

// UI Parity PR6 N5 (May 2026) — unified-diff hunk shape.
//
// A hunk is the section starting with `@@ -<oldStart>,<oldLen> +<newStart>,<newLen> @@ <context>`.
// Each line within carries left + right gutter numbers:
//   ` ` (context)  — both gutters advance
//   `-` (removed)  — only left gutter advances
//   `+` (added)    — only right gutter advances
interface DiffHunkLine {
  text: string;
  kind: EditLineKind;
  // Empty string = no number on that gutter (added line has no left, removed
  // has no right). String type lets the template bind via :data-... without
  // emitting `undefined` strings or 0 sentinels.
  oldLine: string;
  newLine: string;
}

interface DiffHunk {
  header: string; // The literal `@@ -A,B +C,D @@ <ctx>` text.
  oldStart: number;
  newStart: number;
  lines: DiffHunkLine[];
}

const props = withDefaults(defineProps<ToolRendererProps>(), {
  status: "completed",
});

function isAddedLine(line: string): boolean {
  return line.startsWith("+") && !line.startsWith("+++");
}

function isRemovedLine(line: string): boolean {
  return line.startsWith("-") && !line.startsWith("---");
}

function resolveLineKind(
  line: string,
  useDiffFormatting: boolean,
): EditLineKind {
  if (!useDiffFormatting) {
    return "plain";
  }

  if (isAddedLine(line)) {
    return "added";
  }

  if (isRemovedLine(line)) {
    return "removed";
  }

  return "plain";
}

const lines = computed<EditLine[]>(() => {
  const splitLines = props.body.split("\n");
  const useDiffFormatting = splitLines.some(
    (line) => isAddedLine(line) || isRemovedLine(line),
  );

  return splitLines.map((line) => ({
    text: line,
    kind: resolveLineKind(line, useDiffFormatting),
  }));
});

// UI Parity PR6 N5 — parse the body into hunks when `@@` markers are
// present. Falls through to the legacy line-based rendering otherwise.
// The header regex matches the canonical unified-diff form; trailing
// context (function name) after `@@ -A,B +C,D @@` is captured for the
// header label.
const HUNK_HEADER_RE = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$/;

function parseHunks(body: string): DiffHunk[] {
  const lines = body.split("\n");
  const hunks: DiffHunk[] = [];
  let current: DiffHunk | null = null;
  let oldCursor = 0;
  let newCursor = 0;
  for (const rawLine of lines) {
    const headerMatch = rawLine.match(HUNK_HEADER_RE);
    if (headerMatch) {
      if (current) hunks.push(current);
      const oldStart = Number.parseInt(headerMatch[1], 10);
      const newStart = Number.parseInt(headerMatch[3], 10);
      current = {
        header: rawLine,
        oldStart,
        newStart,
        lines: [],
      };
      oldCursor = oldStart;
      newCursor = newStart;
      continue;
    }
    if (!current) continue; // pre-amble lines outside any hunk (file headers etc) — skip
    if (isAddedLine(rawLine)) {
      current.lines.push({
        text: rawLine,
        kind: "added",
        oldLine: "",
        newLine: String(newCursor),
      });
      newCursor += 1;
    } else if (isRemovedLine(rawLine)) {
      current.lines.push({
        text: rawLine,
        kind: "removed",
        oldLine: String(oldCursor),
        newLine: "",
      });
      oldCursor += 1;
    } else {
      // Context line. Strip the leading single space (canonical unified diff)
      // for display, but keep numbering aligned on both gutters.
      current.lines.push({
        text: rawLine,
        kind: "plain",
        oldLine: String(oldCursor),
        newLine: String(newCursor),
      });
      oldCursor += 1;
      newCursor += 1;
    }
  }
  if (current) hunks.push(current);
  return hunks;
}

const hunks = computed<DiffHunk[]>(() => {
  if (!props.body.includes("@@")) return [];
  return parseHunks(props.body);
});

const hasHunks = computed(() => hunks.value.length > 0);
</script>

<template>
  <ToolBubble
    :tool-name="props.toolName"
    :title="props.toolName"
    :subtitle="props.heading"
    :status="props.status"
    :default-open="true"
  >
    <div class="tool-renderer" data-component="edit-tool">
      <div class="tool-renderer__header">
        <span class="tool-renderer__label">Patch</span>
        <CopyButton :text="props.body" />
      </div>
      <!--
        UI Parity PR6 N5 — when the body carries `@@` hunk markers, render
        each hunk as a labelled sub-block with left + right gutter line
        numbers. Otherwise fall through to the legacy flat-line rendering
        below so legacy bodies (plain `-old\n+new`) remain readable.
      -->
      <div v-if="hasHunks" class="tool-code-hunks">
        <div
          v-for="(hunk, hi) in hunks"
          :key="`hunk-${hi}`"
          class="edit-hunk"
          data-testid="edit-hunk"
        >
          <div class="edit-hunk-header" data-testid="edit-hunk-header">
            {{ hunk.header }}
          </div>
          <pre
            class="tool-code tool-code--edit edit-hunk-body"
          ><code><template v-for="(line, li) in hunk.lines" :key="`hunk-${hi}-line-${li}`"><span
              class="edit-line"
              :class="`tool-line--${line.kind}`"
              data-testid="edit-line"
              :data-line-kind="line.kind"
              :data-old-line="line.oldLine"
              :data-new-line="line.newLine"
            ><span class="edit-gutter edit-gutter--old">{{ line.oldLine }}</span><span class="edit-gutter edit-gutter--new">{{ line.newLine }}</span><span class="edit-line-text">{{ line.text }}</span></span></template></code></pre>
        </div>
      </div>
      <pre v-else class="tool-code tool-code--edit"><code>
<template v-for="(line, index) in lines" :key="`${index}-${line.text}`"><span class="tool-line" :class="`tool-line--${line.kind}`" :data-line-kind="line.kind">{{ line.text }}</span>
<br v-if="index < lines.length - 1" /></template></code></pre>
    </div>
  </ToolBubble>
</template>

<style scoped>
.tool-renderer {
  display: grid;
  gap: 0.45rem;
}

.tool-renderer__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
}

.tool-renderer__label {
  color: var(--text-secondary, #a9b1d6);
  font-size: 0.78rem;
  font-weight: 600;
  text-transform: uppercase;
}

.tool-code {
  margin: 0;
  padding: 0.85rem 1rem;
  border: 1px solid var(--border, rgba(148, 163, 184, 0.25));
  border-radius: calc(var(--radius, 12px) - 4px);
  background: var(--surface-low, #1a1b26);
  color: var(--text-primary, #c0caf5);
  font-family:
    ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono",
    "Courier New", monospace;
  font-size: 0.85rem;
  line-height: 1.5;
  overflow-x: auto;
  white-space: pre-wrap;
  word-break: break-word;
}

.tool-line--added {
  display: inline;
  color: #9ece6a;
}

.tool-line--removed {
  display: inline;
  color: var(--error, #f7768e);
}

.tool-line--plain {
  display: inline;
  color: var(--text-primary, #c0caf5);
}

/*
 * UI Parity PR6 N5 — hunk separation + line-number gutters.
 *
 * Each hunk is its own bordered block so multi-hunk diffs read as
 * separate chunks of work. The header carries the canonical `@@ -A,B +C,D @@`
 * marker in a muted strip above the content. Inside each hunk every line
 * gets a left and a right gutter; the gutters use a fixed-width column so
 * numbers stay aligned and the prefix glyph (+/-/space) sits cleanly to
 * the right of the gutters.
 */
.tool-code-hunks {
  display: grid;
  gap: 0.5rem;
}

.edit-hunk {
  border: 1px solid var(--border, rgba(148, 163, 184, 0.25));
  border-radius: calc(var(--radius, 12px) - 4px);
  overflow: hidden;
}

.edit-hunk-header {
  padding: 0.35rem 0.85rem;
  background: var(--surface-mid, rgba(148, 163, 184, 0.08));
  color: var(--text-muted, #565f89);
  font-family:
    ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono",
    "Courier New", monospace;
  font-size: 0.78rem;
  border-bottom: 1px solid var(--border, rgba(148, 163, 184, 0.25));
}

.edit-hunk-body {
  margin: 0;
  border: 0;
  border-radius: 0;
}

.edit-line {
  display: grid;
  grid-template-columns: 3ch 3ch 1fr;
  gap: 0.5rem;
  align-items: baseline;
  padding: 0 0.5rem;
  white-space: pre-wrap;
}

.edit-line.tool-line--added {
  background: rgba(158, 206, 106, 0.08);
  color: #9ece6a;
}

.edit-line.tool-line--removed {
  background: rgba(247, 118, 142, 0.08);
  color: var(--error, #f7768e);
}

.edit-line.tool-line--plain {
  color: var(--text-primary, #c0caf5);
}

.edit-gutter {
  color: var(--text-muted, #565f89);
  font-size: 0.7rem;
  text-align: right;
  user-select: none;
}

.edit-line-text {
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
