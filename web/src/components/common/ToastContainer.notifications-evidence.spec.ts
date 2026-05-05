/**
 * ToastContainer.notifications-evidence.spec.ts
 *
 * Live DOM evidence for the May 2026 user-facing-notifications work.
 * Mounts the real ToastContainer, drives the chatStore through the
 * actual SSE-event applyContentEvent path, and writes the rendered HTML
 * to /tmp/notifications-evidence/ at each significant frame.
 *
 * This is an evidence harness, not a behavioural test — assertions are
 * minimal (the dom snapshot is the deliverable). The behavioural tests
 * live in chatStore.test.ts and useToast.spec.ts.
 */
import { afterEach, beforeEach, describe, it, expect, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { writeFileSync, mkdirSync } from 'node:fs'
import ToastContainer from './ToastContainer.vue'
import { useToast } from '@/composables/useToast'
import { useChatStore } from '@/stores/chatStore'

const EVIDENCE_DIR = '/tmp/notifications-evidence'

function captureDom(label: string): string {
  const html = document.body.innerHTML
  const wrapped = `<!doctype html>
<meta charset="utf-8">
<title>${label}</title>
<style>
  body { background: #1a1a1a; color: #e5e5e5; font-family: -apple-system, system-ui, sans-serif; padding: 2rem; }
  .toast-container { position: static !important; }
  .toast-item { background: #2a2a2a; border: 1px solid #3a3a3a; border-radius: 8px; padding: 0.75rem 1rem; display: flex; justify-content: space-between; gap: 0.75rem; margin: 0.5rem 0; max-width: 400px; }
  .toast-item--loading { border-color: #6366f1; }
  .toast-item--error { background: rgba(220, 38, 38, 0.15); border-color: rgba(220, 38, 38, 0.3); }
  .toast-title { font-weight: 600; font-size: 0.9rem; display: block; margin-bottom: 0.25rem; }
  .toast-message { font-size: 0.85rem; }
  .toast-close-btn { background: transparent; border: none; color: #888; font-size: 1.1rem; cursor: pointer; }
  h1 { font-size: 1rem; color: #888; }
</style>
<h1>${label}</h1>
${html}`
  mkdirSync(EVIDENCE_DIR, { recursive: true })
  const safeLabel = label.replace(/[^a-z0-9-]+/gi, '-').toLowerCase()
  writeFileSync(`${EVIDENCE_DIR}/${safeLabel}.html`, wrapped)
  return html
}

describe('Notifications evidence harness (writes /tmp/notifications-evidence/*.html)', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    const { dismissAll } = useToast()
    dismissAll()
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
    const { dismissAll } = useToast()
    dismissAll()
  })

  it('captures: 01 single tool_call → "Running command" toast', async () => {
    mount(ToastContainer, { attachTo: document.body })
    const store = useChatStore()
    store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'bash', status: 'running' }))
    await flushPromises()

    const html = captureDom('01-single-tool-bash')
    expect(html).toContain('Running command')
    expect(html).toContain('toast-item--loading')
  })

  it('captures: 02 multi-tool burst → aggregated "+ N more" toast', async () => {
    mount(ToastContainer, { attachTo: document.body })
    const store = useChatStore()
    store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'Read', status: 'running' }))
    store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'Grep', status: 'running' }))
    store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'Edit', status: 'running' }))
    store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'Bash', status: 'running' }))
    store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: 'Write', status: 'running' }))
    await flushPromises()

    const html = captureDom('02-multi-tool-burst')
    expect(html).toContain('Reading file')
    expect(html).toContain('+ 4 more')
    // Only ONE toast despite 5 tool_call events.
    const toastCount = (html.match(/toast-item--/g) || []).length
    expect(toastCount).toBe(1)
  })

  it('captures: 03 provider_changed → failover toast (existing behaviour)', async () => {
    mount(ToastContainer, { attachTo: document.body })
    const store = useChatStore()
    store.currentProviderId = 'anthropic'
    store.currentModelId = 'claude-sonnet-4-6'
    store.applyContentEvent(
      JSON.stringify({
        type: 'provider_changed',
        from: 'anthropic+claude-sonnet-4-6',
        to: 'zai+glm-4.6',
        reason: 'rate_limited',
      }),
    )
    await flushPromises()

    const html = captureDom('03-provider-changed-failover')
    expect(html).toContain('Switched to glm-4.6')
    expect(html).toContain('rate-limited')
  })

  it('captures: 04 model_active when actual differs from chip → "Now answering with" toast', async () => {
    mount(ToastContainer, { attachTo: document.body })
    const store = useChatStore()
    // User picked claude on anthropic; manifest pin pivots to glm-4.6.
    store.currentProviderId = 'anthropic'
    store.currentModelId = 'claude-sonnet-4-6'
    store.applyContentEvent(
      JSON.stringify({ type: 'model_active', provider: 'zai', model: 'glm-4.6' }),
    )
    await flushPromises()

    const html = captureDom('04-model-active-divergent')
    expect(html).toContain('Now answering with glm-4.6')
  })

  it('captures: 05 model_active matching chip → no toast (silent)', async () => {
    mount(ToastContainer, { attachTo: document.body })
    const store = useChatStore()
    store.currentProviderId = 'anthropic'
    store.currentModelId = 'claude-sonnet-4-6'
    store.applyContentEvent(
      JSON.stringify({ type: 'model_active', provider: 'anthropic', model: 'claude-sonnet-4-6' }),
    )
    await flushPromises()

    const html = captureDom('05-model-active-matching-silent')
    // No toast item rendered — selection matched actual.
    expect(html.match(/toast-item--/g)).toBeNull()
  })

  it('captures: 06 provider_changed dedup suppresses follow-up model_active toast', async () => {
    mount(ToastContainer, { attachTo: document.body })
    const store = useChatStore()
    store.currentProviderId = 'anthropic'
    store.currentModelId = 'claude-sonnet-4-6'

    // Failover sequence on the wire: provider_changed → model_active.
    store.applyContentEvent(
      JSON.stringify({
        type: 'provider_changed',
        from: 'anthropic+claude-sonnet-4-6',
        to: 'zai+glm-4.6',
        reason: 'rate_limited',
      }),
    )
    store.applyContentEvent(
      JSON.stringify({ type: 'model_active', provider: 'zai', model: 'glm-4.6' }),
    )
    await flushPromises()

    const html = captureDom('06-provider-changed-dedup-no-doublefire')
    // Only ONE toast — provider_changed's rich copy, model_active stayed silent.
    const toastCount = (html.match(/toast-item--/g) || []).length
    expect(toastCount).toBe(1)
    expect(html).toContain('Switched to glm-4.6')
    expect(html).not.toContain('Now answering with')
  })

  it('captures: 07 friendly tool labels for all common tools', async () => {
    mount(ToastContainer, { attachTo: document.body })
    const store = useChatStore()
    // Single tool_call per snapshot point — drive each in isolation
    // and compose the final html as the union of frames.
    const samples = [
      { name: 'bash', expected: 'Running command' },
      { name: 'Read', expected: 'Reading file' },
      { name: 'Edit', expected: 'Editing file' },
      { name: 'Grep', expected: 'Searching files' },
      { name: 'WebFetch', expected: 'Fetching web page' },
      { name: 'Task', expected: 'Delegating to agent' },
      { name: 'TodoWrite', expected: 'Updating to-dos' },
    ]
    const frames: string[] = []
    for (const sample of samples) {
      const { dismissAll } = useToast()
      dismissAll()
      store.dismissToolActivityToast?.()
      store.applyContentEvent(JSON.stringify({ type: 'tool_call', name: sample.name, status: 'running' }))
      await flushPromises()
      const frameHtml = document.body.innerHTML
      frames.push(`<section><h2 style="font-size:0.85rem;color:#aaa">${sample.name} → ${sample.expected}</h2>${frameHtml}</section>`)
      expect(frameHtml).toContain(sample.expected)
    }

    const composite = `<!doctype html>
<meta charset="utf-8">
<title>07 — Friendly tool labels</title>
<style>
  body { background: #1a1a1a; color: #e5e5e5; font-family: -apple-system, system-ui, sans-serif; padding: 2rem; }
  .toast-container { position: static !important; }
  .toast-item { background: #2a2a2a; border: 1px solid #3a3a3a; border-radius: 8px; padding: 0.75rem 1rem; display: flex; justify-content: space-between; gap: 0.75rem; margin: 0.5rem 0; max-width: 400px; }
  .toast-item--loading { border-color: #6366f1; }
  .toast-title { font-weight: 600; font-size: 0.9rem; display: block; margin-bottom: 0.25rem; }
  .toast-message { font-size: 0.85rem; }
  .toast-close-btn { background: transparent; border: none; color: #888; font-size: 1.1rem; cursor: pointer; }
  section { margin-bottom: 1.5rem; }
  h1 { font-size: 1rem; color: #888; }
</style>
<h1>07 — Friendly tool labels (one frame per tool)</h1>
${frames.join('\n')}`
    mkdirSync(EVIDENCE_DIR, { recursive: true })
    writeFileSync(`${EVIDENCE_DIR}/07-friendly-tool-labels.html`, composite)
  })
})
