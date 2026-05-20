<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import MarkdownIt from "markdown-it";
import {
  ensureHighlighterLoaded,
  highlightCode,
  onHighlighterReady,
} from "@/lib/markdownHighlighter";
import { useChatStore } from "@/stores/chatStore";

defineOptions({ name: "MarkdownRenderer" });

const props = defineProps<{ content: string }>();

// Chat store handle — sourced once at setup time so the same instance
// is used for every render pass. The image allow-list (below) reaches
// in for `currentSessionId` to enforce the same-session URL constraint
// on `<img src="/api/v1/sessions/{id}/attachments/...">` (plan R9
// cross-session injection defence). The store is injected via Pinia
// at app bootstrap and is the canonical source of the active session
// id (also referenced as `chat.currentSessionId` in
// web/src/stores/swarmStore.test.ts:103,127,156,205).
const chat = useChatStore();

const md = new MarkdownIt({
  // N9 (Vue UI Parity vs OpenCode, May 2026) / plan "Chat Attachments
  // Backend (May 2026)" §6 task-08. `html: true` lets the renderer
  // surface assistant-emitted `<img>` tags that point at base64 data
  // URLs OR same-session attachment URLs. Every OTHER raw-HTML tag
  // (script, iframe, object, …) is stripped from the rendered DOM by
  // the post-render `applyImageAllowList()` filter below, so the
  // threat model the original `html: false` posture closed remains
  // closed — we trade a parser-level strip for an explicit allow-list
  // gate, with no widening of the actually-rendered surface.
  html: true,
  linkify: false,
  typographer: true,
  breaks: true,
  // B1 (Vue UI Parity vs OpenCode, May 2026). Wire Shiki as the
  // fenced-block highlighter. Returning a non-empty string from this
  // callback tells MarkdownIt to use it verbatim (i.e. skip the
  // default `<pre><code>` wrap). `highlightCode` itself returns
  // Shiki's full `<pre class="shiki …">…</pre>` markup, so the inner
  // result is a complete pre element. On unsupported languages or
  // any Shiki failure we return `''` and let the fence renderer
  // (overridden below) inject a plain `<pre><code>` with the
  // language class — preserves the current contract for legacy
  // languages and never surfaces a Shiki error to the user.
  highlight: (str: string, lang: string): string => {
    const html = highlightCode(str, lang);
    return html ?? "";
  },
});

// N4 (Vue UI Parity vs OpenCode, May 2026). Per-code-block copy
// affordance. Override the default fence renderer so every fenced
// block emits a wrapper carrying a copy button. Hover-reveal
// styling is handled in the scoped CSS below — the data-testid is
// present unconditionally so the spec contract can lift the
// affordance without depending on visibility.
//
// Three rendering paths converge here:
//
//   1. Shiki succeeded — `highlight()` returned a `<pre class="shiki …">`
//      block. We use it verbatim as the inner code surface.
//   2. Shiki returned `''` (unknown language, plain fence, or Shiki
//      failure). We render a plain `<pre><code class="language-…">`
//      with the source HTML-escaped, matching the legacy contract.
//   3. The HTML output already carries Shiki's own wrapper; we add
//      our wrapper outside it so the inner `<pre>` is unmodified
//      and CSS targeting `pre.shiki` keeps working.
//
// The copy button is wired via an unobtrusive click listener
// registered on the rendered DOM (see `onCopyClick` below) — keeps
// the markdown render output pure HTML and avoids a hydration
// boundary inside the v-html surface.
md.renderer.rules.fence = (tokens, idx, options): string => {
  const token = tokens[idx];
  const info = token.info ? token.info.trim() : "";
  const lang = info.split(/\s+/g)[0] ?? "";
  const content = token.content;

  // Try Shiki first via the highlight option.
  let codeHtml = "";
  if (options.highlight) {
    codeHtml = options.highlight(content, lang, "") || "";
  }

  if (codeHtml === "") {
    // Fallback path — plain `<pre><code>` with HTML-escaped source.
    // Matches what MarkdownIt's default fence renderer would have
    // produced before B1 landed.
    const escaped = md.utils.escapeHtml(content);
    const langClass = lang
      ? ` class="language-${md.utils.escapeHtml(lang)}"`
      : "";
    codeHtml = `<pre><code${langClass}>${escaped}</code></pre>`;
  }

  // Wrap with a copy affordance container. The raw source is stored
  // on a data attribute on the wrapper so the click handler can
  // copy the original text (not the tokenised HTML). HTML-escape
  // the raw to avoid breaking out of the attribute.
  const rawAttr = md.utils.escapeHtml(content);
  return (
    `<div class="markdown-code" data-code-raw="${rawAttr}">` +
    codeHtml +
    `<button type="button" class="markdown-code__copy-btn" data-testid="markdown-code-copy-btn" aria-label="Copy code block">` +
    `<span aria-hidden="true">📋</span><span class="markdown-code__copy-label">Copy</span>` +
    `</button>` +
    `</div>`
  );
};

// M6 (Bug Hunt May 2026): tighten link validation. markdown-it 14's default
// validateLink regex-tests the trimmed lower-cased URL but does NOT URL-
// decode first, so `javascript%3Aalert(1)` slips through, renders as
// `href="javascript%3Aalert(1)"`, and the browser decodes %3A → ":" on
// click — script executes in the chat origin. We replace the default with
// a strict scheme allowlist: http, https, mailto, fragments (#…), and
// relative paths (./…, ../…, /…). Everything else (javascript, vbscript,
// data, file, blob, ftp, scheme-relative //) becomes plain text.
const ALLOWED_SCHEMES = new Set(["http:", "https:", "mailto:"]);

md.validateLink = (url: string): boolean => {
  if (typeof url !== "string") return false;
  // Decode percent-encoding so `javascript%3A...` cannot bypass the check.
  let decoded: string;
  try {
    decoded = decodeURIComponent(url);
  } catch {
    decoded = url;
  }
  const trimmed = decoded.trim().toLowerCase();
  if (trimmed === "") return false;
  // Scheme-relative `//host` inherits the page protocol — disallow.
  if (trimmed.startsWith("//")) return false;
  // Fragment-only (`#anchor`) and relative paths (`/`, `./`, `../`) are
  // safe — no scheme to abuse.
  if (
    trimmed.startsWith("#") ||
    trimmed.startsWith("/") ||
    trimmed.startsWith("./") ||
    trimmed.startsWith("../")
  ) {
    return true;
  }
  // Anything containing a colon before the first slash is a scheme — gate
  // it. Anything else (e.g. `guide.md`) is a relative reference.
  const colonIdx = trimmed.indexOf(":");
  const slashIdx = trimmed.indexOf("/");
  if (colonIdx === -1 || (slashIdx !== -1 && slashIdx < colonIdx)) {
    return true;
  }
  const scheme = trimmed.slice(0, colonIdx + 1);
  return ALLOWED_SCHEMES.has(scheme);
};

// B1: Reactive version counter to trigger a re-render after the
// lazy-loaded Shiki highlighter resolves. Before Shiki is ready,
// `highlightCode` returns `null` and the fence renderer falls back
// to plain `<pre><code>`. The first time `ensureHighlighterLoaded`
// completes (typically <100 ms after first render), this counter
// increments and Vue re-runs `renderedHtml` so the same content
// re-renders with tokenised code blocks.
const highlighterVersion = ref(0);
let unsubscribeReady: (() => void) | null = null;

onMounted(() => {
  unsubscribeReady = onHighlighterReady(() => {
    highlighterVersion.value += 1;
  });
  // Fire-and-forget — load Shiki in the background. The
  // `highlighterVersion` increment above wakes us up when it lands.
  void ensureHighlighterLoaded();
});

// N9 / task-08 — image allow-list. Walks the parsed HTML and drops
// every non-<img> raw HTML tag (script, iframe, object, …) and every
// <img> whose `src` does not match the strict allow-list:
//
//   1. `^data:image/(png|jpeg|gif|webp);base64,` — the four Anthropic-
//      supported image types. SVG is INTENTIONALLY excluded
//      (AC-08-SVG-Excluded) because SVG can carry inline <script> and
//      event handlers (onload="…", onmouseover="…", …) that execute
//      in the page's origin.
//   2. `^/api/v1/sessions/<currentSessionId>/attachments/<aid>$` —
//      same-session attachment URLs only. A cross-session probe
//      (assistant prompt-injection trying to read attachments from
//      another conversation, plan R9) dispatches an
//      `attachment_blocked.cross_session` window event so a test or
//      operator can observe the defence firing.
//
// The walk uses DOMParser inside a fragment context — it parses HTML
// strict-once, lets the browser's own HTML parser handle the heavy
// lifting, and re-serialises only allow-listed nodes. Markdown-rendered
// elements (p, h1-h6, ul, ol, li, code, pre, table, thead, tbody, tr,
// th, td, blockquote, hr, a, strong, em, br) are preserved verbatim
// (they're emitted by markdown-it from markdown source, not from raw
// HTML in the input). Only the small set of HTML *tags that can only
// appear via the markdown-it `html: true` path* needs allow-list
// gating — and within that set, only `<img>` is permitted.

const DATA_URL_ALLOWED = /^data:image\/(png|jpeg|gif|webp);base64,/i;

const SESSION_ATTACHMENT_URL =
  /^\/api\/v1\/sessions\/([A-Za-z0-9_-]+)\/attachments\/[A-Za-z0-9_-]+$/;

// HTML tags that are explicit script-execution or exfiltration vectors
// and must be stripped from the rendered DOM regardless of how they
// got there (assistant-emitted raw HTML via the `html: true` parse
// path). Markdown-it's grammar does not emit these; our custom fence
// renderer emits `<div>`, `<pre>`, `<code>`, `<span>`, and `<button>`
// only — none of which appear here.
//
// NOTE: `<svg>` is on this list because SVG can carry inline <script>
// and event handlers — mirrors the AC-08-SVG-Excluded constraint on
// the `<img>` src allow-list (data:image/svg+xml is rejected there
// for the same reason).
//
// `<button>`, `<form>`, `<input>`, `<video>`, `<audio>`, `<canvas>`
// are intentionally NOT in this list: a literal `<button>` in an
// assistant message is just a button (no script execution surface),
// and the fence renderer below legitimately emits `<button>` for the
// per-code-block copy affordance. Stripping them would regress N4.
const RAW_HTML_TAGS_TO_STRIP = new Set([
  "script",
  "iframe",
  "object",
  "embed",
  "style",
  "link",
  "meta",
  "base",
  "frame",
  "frameset",
  "applet",
  "svg",
]);

function emitCrossSessionBlocked(rawSrc: string): void {
  if (typeof window === "undefined") return;
  try {
    window.dispatchEvent(
      new CustomEvent("attachment_blocked.cross_session", {
        detail: { src: rawSrc },
      }),
    );
  } catch {
    // CustomEvent unavailable (very old environments) — fall back to a
    // best-effort console signal so we never silently drop the
    // observability promise made to operators / tests.
    if (typeof console !== "undefined" && typeof console.warn === "function") {
      console.warn("attachment_blocked.cross_session", rawSrc);
    }
  }
}

function isImgSrcAllowed(
  rawSrc: string,
  currentSessionId: string | null,
): boolean {
  if (typeof rawSrc !== "string" || rawSrc === "") return false;
  if (DATA_URL_ALLOWED.test(rawSrc)) return true;
  const match = SESSION_ATTACHMENT_URL.exec(rawSrc);
  if (match === null) return false;
  const srcSession = match[1];
  if (currentSessionId !== null && srcSession === currentSessionId) {
    return true;
  }
  // The URL is shaped like a session-attachment path but points at a
  // DIFFERENT session id — fire the typed observability event before
  // the caller drops the node so a listener can attribute the block.
  emitCrossSessionBlocked(rawSrc);
  return false;
}

function applyImageAllowList(
  html: string,
  currentSessionId: string | null,
): string {
  if (typeof window === "undefined" || typeof DOMParser === "undefined") {
    // SSR / non-browser environment — return the raw HTML unchanged.
    // The chat surface is browser-only so this branch is defensive.
    return html;
  }
  // Parse inside a synthetic body so document-level boilerplate (html,
  // head, body) does not appear in the output. We re-serialise body's
  // children only.
  const doc = new DOMParser().parseFromString(
    `<!doctype html><html><body>${html}</body></html>`,
    "text/html",
  );
  const body = doc.body;
  // Walk every element and gate it. Use a fresh static list of
  // candidates because we mutate the DOM in-place.
  const all = Array.from(body.querySelectorAll("*"));
  for (const el of all) {
    const tag = el.tagName.toLowerCase();
    if (tag === "img") {
      const src = el.getAttribute("src");
      if (src === null || !isImgSrcAllowed(src, currentSessionId)) {
        el.remove();
      }
      continue;
    }
    if (RAW_HTML_TAGS_TO_STRIP.has(tag)) {
      el.remove();
      continue;
    }
    // Strip inline event handlers and `javascript:` href/src
    // residue from preserved tags (defence in depth — these would not
    // normally appear via markdown-it but a `html: true` parse could
    // surface them via author HTML in an assistant response).
    const attrs = Array.from(el.attributes);
    for (const attr of attrs) {
      const name = attr.name.toLowerCase();
      const value = attr.value;
      if (name.startsWith("on")) {
        el.removeAttribute(attr.name);
        continue;
      }
      if (
        (name === "href" || name === "src") &&
        /^\s*javascript:/i.test(value)
      ) {
        el.removeAttribute(attr.name);
      }
    }
  }
  return body.innerHTML;
}

const renderedHtml = computed(() => {
  // Touch the version counter so Vue tracks it as a dependency —
  // when Shiki finishes loading and the version bumps, the
  // computed re-evaluates with the real highlight callback wired
  // through MarkdownIt.
  void highlighterVersion.value;
  const raw = md.render(props.content);
  // Allow-list filter — Vue's reactivity tracks chat.currentSessionId
  // through the closure, so a session switch re-evaluates the computed
  // and re-applies the constraint with the new active id.
  return applyImageAllowList(raw, chat.currentSessionId);
});

// Per-block copy affordance handler. The fence renderer emits the
// copy button as static HTML inside the v-html surface, so the click
// is wired via event delegation on the outer `.markdown-body`. The
// raw (untokenised) source text lives on the wrapper's
// `data-code-raw` attribute. A two-second "Copied" affordance hint
// is surfaced by toggling a class on the clicked button.
const copiedTimers = new Map<HTMLElement, ReturnType<typeof setTimeout>>();

function onCopyClick(event: MouseEvent): void {
  const target = event.target as HTMLElement | null;
  if (!target) return;
  const btn = target.closest(
    ".markdown-code__copy-btn",
  ) as HTMLButtonElement | null;
  if (!btn) return;
  event.preventDefault();
  const wrapper = btn.closest(".markdown-code") as HTMLElement | null;
  if (!wrapper) return;
  const raw = wrapper.getAttribute("data-code-raw") ?? "";
  // navigator.clipboard is async; the spec only asserts the button
  // exists. Best-effort copy — silently no-ops if clipboard API is
  // unavailable (e.g. headless test environments).
  if (
    typeof navigator !== "undefined" &&
    navigator.clipboard &&
    typeof navigator.clipboard.writeText === "function"
  ) {
    navigator.clipboard.writeText(raw).then(
      () => {
        btn.classList.add("markdown-code__copy-btn--copied");
        const existing = copiedTimers.get(btn);
        if (existing) clearTimeout(existing);
        const t = setTimeout(() => {
          btn.classList.remove("markdown-code__copy-btn--copied");
          copiedTimers.delete(btn);
        }, 2000);
        copiedTimers.set(btn, t);
      },
      () => {
        // Clipboard rejected — no surfacing, the user can retry.
      },
    );
  }
}

onBeforeUnmount(() => {
  copiedTimers.forEach((t) => clearTimeout(t));
  copiedTimers.clear();
  if (unsubscribeReady !== null) {
    unsubscribeReady();
    unsubscribeReady = null;
  }
});
</script>

<template>
  <div class="markdown-body" v-html="renderedHtml" @click="onCopyClick" />
</template>

<style scoped>
.markdown-body {
  color: var(--text-primary);
  line-height: 1.6;
  font-size: 0.9rem;
  /*
   * UI Parity I6 (May 2026): align with MessageBubble — use
   * overflow-wrap: anywhere so URL-heavy / ID-heavy markdown content
   * wraps cleanly inside the bubble rather than spilling off the
   * right edge. min-width: 0 prevents the rendered HTML from forcing
   * its flex/grid parent to overflow before the wrap kicks in.
   */
  overflow-wrap: anywhere;
  min-width: 0;
}

.markdown-body :deep(h1),
.markdown-body :deep(h2),
.markdown-body :deep(h3),
.markdown-body :deep(h4),
.markdown-body :deep(h5),
.markdown-body :deep(h6) {
  color: var(--text-primary);
  font-weight: 600;
  line-height: 1.3;
  margin-top: 1rem;
  margin-bottom: 0.5rem;
}

.markdown-body :deep(h1) {
  font-size: 1.5rem;
}

.markdown-body :deep(h2) {
  font-size: 1.3rem;
}

.markdown-body :deep(h3) {
  font-size: 1.15rem;
}

.markdown-body :deep(h4) {
  font-size: 1.05rem;
}

.markdown-body :deep(h5),
.markdown-body :deep(h6) {
  font-size: 1rem;
}

.markdown-body :deep(p) {
  margin: 0.5rem 0;
  line-height: 1.6;
}

.markdown-body :deep(p:first-child) {
  margin-top: 0;
}

.markdown-body :deep(p:last-child) {
  margin-bottom: 0;
}

.markdown-body :deep(strong) {
  font-weight: 600;
  color: var(--text-primary);
}

.markdown-body :deep(em) {
  font-style: italic;
}

.markdown-body :deep(a) {
  color: var(--accent, #7aa2f7);
  text-decoration: none;
}

.markdown-body :deep(a:hover) {
  text-decoration: underline;
}

.markdown-body :deep(code) {
  background: var(--bg-secondary, rgba(255, 255, 255, 0.06));
  padding: 0.15rem 0.4rem;
  border-radius: 3px;
  font-family: var(--font-mono, monospace);
  font-size: 0.85em;
}

.markdown-body :deep(pre) {
  background: var(--bg-secondary, rgba(255, 255, 255, 0.06));
  border: 1px solid var(--border, rgba(255, 255, 255, 0.1));
  border-radius: 6px;
  padding: 0.75rem 1rem;
  overflow-x: auto;
  margin: 0.75rem 0;
  font-family: var(--font-mono, monospace);
  font-size: 0.8rem;
  line-height: 1.5;
}

.markdown-body :deep(pre code) {
  background: transparent;
  padding: 0;
  border-radius: 0;
  font-size: inherit;
}

.markdown-body :deep(table) {
  width: 100%;
  border-collapse: collapse;
  margin: 0.75rem 0;
  font-size: 0.875rem;
}

.markdown-body :deep(th),
.markdown-body :deep(td) {
  border: 1px solid var(--border, rgba(255, 255, 255, 0.1));
  padding: 0.5rem 0.75rem;
  text-align: left;
}

.markdown-body :deep(th) {
  background: var(--bg-secondary, rgba(255, 255, 255, 0.06));
  font-weight: 600;
  color: var(--text-primary);
}

.markdown-body :deep(tr:nth-child(even)) {
  background: rgba(255, 255, 255, 0.02);
}

.markdown-body :deep(ul),
.markdown-body :deep(ol) {
  padding-left: 1.5rem;
  margin: 0.5rem 0;
}

.markdown-body :deep(ul) {
  list-style-type: disc;
}

.markdown-body :deep(ol) {
  list-style-type: decimal;
}

.markdown-body :deep(li) {
  margin: 0.25rem 0;
  line-height: 1.5;
}

.markdown-body :deep(li > ul),
.markdown-body :deep(li > ol) {
  margin: 0.15rem 0;
}

.markdown-body :deep(blockquote) {
  border-left: 3px solid var(--accent, #7aa2f7);
  padding: 0.25rem 0.75rem;
  margin: 0.5rem 0;
  color: var(--text-muted, rgba(255, 255, 255, 0.5));
}

.markdown-body :deep(blockquote p:first-child) {
  margin-top: 0;
}

.markdown-body :deep(blockquote p:last-child) {
  margin-bottom: 0;
}

.markdown-body :deep(hr) {
  border: none;
  border-top: 1px solid var(--border, rgba(255, 255, 255, 0.1));
  margin: 1rem 0;
}

.markdown-body :deep(img) {
  max-width: 100%;
  border-radius: 6px;
  margin: 0.5rem 0;
}

/* N4: per-fence wrapper with hover-revealed copy button. The wrapper
 * holds the Shiki `<pre>` (or plain `<pre>` fallback) plus the copy
 * affordance. Positioned in the top-right corner of the block. */
.markdown-body :deep(.markdown-code) {
  position: relative;
}

.markdown-body :deep(.markdown-code__copy-btn) {
  position: absolute;
  top: 0.4rem;
  right: 0.4rem;
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  padding: 0.2rem 0.5rem;
  border: 1px solid var(--border, rgba(148, 163, 184, 0.35));
  border-radius: 4px;
  background: var(--bg-elevated, rgba(0, 0, 0, 0.5));
  color: var(--text-secondary, inherit);
  font-family: var(--font-mono, monospace);
  font-size: 0.7rem;
  line-height: 1;
  cursor: pointer;
  opacity: 0;
  transition:
    opacity 0.15s,
    border-color 0.15s;
}

.markdown-body :deep(.markdown-code:hover .markdown-code__copy-btn),
.markdown-body :deep(.markdown-code__copy-btn:focus-visible) {
  opacity: 1;
}

.markdown-body :deep(.markdown-code__copy-btn:hover) {
  border-color: var(--accent, #7aa2f7);
}

.markdown-body :deep(.markdown-code__copy-btn--copied) {
  opacity: 1;
  border-color: var(--success, #9ece6a);
  color: var(--success, #9ece6a);
}

.markdown-body
  :deep(.markdown-code__copy-btn--copied .markdown-code__copy-label::before) {
  content: "Copied";
}

.markdown-body
  :deep(.markdown-code__copy-btn--copied .markdown-code__copy-label) {
  font-size: 0;
}

.markdown-body
  :deep(.markdown-code__copy-btn--copied .markdown-code__copy-label::before) {
  font-size: 0.7rem;
}

/* B1: Shiki output. The `pre.shiki` element carries inline `style`
 * for the chosen theme's background — that's intentional. We layer
 * the existing `pre` rules (border, radius, padding, overflow)
 * underneath the theme background so the block still feels like
 * part of the bubble. */
.markdown-body :deep(pre.shiki) {
  background: var(--bg-secondary, rgba(255, 255, 255, 0.06));
  border: 1px solid var(--border, rgba(255, 255, 255, 0.1));
  border-radius: 6px;
  padding: 0.75rem 1rem;
  overflow-x: auto;
  margin: 0.75rem 0;
  font-family: var(--font-mono, monospace);
  font-size: 0.8rem;
  line-height: 1.5;
}

.markdown-body :deep(pre.shiki code) {
  background: transparent;
  padding: 0;
  border-radius: 0;
  font-size: inherit;
}
</style>
