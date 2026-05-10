<script setup lang="ts">
import { computed } from 'vue'
import MarkdownIt from 'markdown-it'

defineOptions({ name: 'MarkdownRenderer' })

const props = defineProps<{ content: string }>()

const md = new MarkdownIt({
  html: false,
  linkify: false,
  typographer: true,
  breaks: true,
})

// M6 (Bug Hunt May 2026): tighten link validation. markdown-it 14's default
// validateLink regex-tests the trimmed lower-cased URL but does NOT URL-
// decode first, so `javascript%3Aalert(1)` slips through, renders as
// `href="javascript%3Aalert(1)"`, and the browser decodes %3A → ":" on
// click — script executes in the chat origin. We replace the default with
// a strict scheme allowlist: http, https, mailto, fragments (#…), and
// relative paths (./…, ../…, /…). Everything else (javascript, vbscript,
// data, file, blob, ftp, scheme-relative //) becomes plain text.
const ALLOWED_SCHEMES = new Set(['http:', 'https:', 'mailto:'])

md.validateLink = (url: string): boolean => {
  if (typeof url !== 'string') return false
  // Decode percent-encoding so `javascript%3A...` cannot bypass the check.
  let decoded: string
  try {
    decoded = decodeURIComponent(url)
  } catch {
    decoded = url
  }
  const trimmed = decoded.trim().toLowerCase()
  if (trimmed === '') return false
  // Scheme-relative `//host` inherits the page protocol — disallow.
  if (trimmed.startsWith('//')) return false
  // Fragment-only (`#anchor`) and relative paths (`/`, `./`, `../`) are
  // safe — no scheme to abuse.
  if (
    trimmed.startsWith('#') ||
    trimmed.startsWith('/') ||
    trimmed.startsWith('./') ||
    trimmed.startsWith('../')
  ) {
    return true
  }
  // Anything containing a colon before the first slash is a scheme — gate
  // it. Anything else (e.g. `guide.md`) is a relative reference.
  const colonIdx = trimmed.indexOf(':')
  const slashIdx = trimmed.indexOf('/')
  if (colonIdx === -1 || (slashIdx !== -1 && slashIdx < colonIdx)) {
    return true
  }
  const scheme = trimmed.slice(0, colonIdx + 1)
  return ALLOWED_SCHEMES.has(scheme)
}

const renderedHtml = computed(() => md.render(props.content))
</script>

<template>
  <div class="markdown-body" v-html="renderedHtml" />
</template>

<style scoped>
.markdown-body {
  color: var(--text-primary);
  line-height: 1.6;
  font-size: 0.9rem;
  word-break: break-word;
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
</style>
