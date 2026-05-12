import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ContextUsageChip from './ContextUsageChip.vue'
import { useChatStore } from '@/stores/chatStore'

/**
 * ContextUsageChip component specs — pin BEHAVIOUR observable to a
 * user, not internal call signatures. The chip is the UI affordance
 * for the engine's `context_usage` SSE event class (see Bug Fixes /
 * glm Context-Window Saturation Detection in the vault). The chat
 * store owns the source-of-truth state (`currentContextUsage`); the
 * chip renders it.
 */
describe('ContextUsageChip', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    // Phase 3 — visibility predicate now requires a selected model.
    // Existing specs that exercise figure rendering / severity all
    // assume a model is selected, so seed it once here. Specs that
    // exercise the no-model edge override this explicitly.
    const store = useChatStore()
    store.currentModelId = 'glm-4.6'
  })

  it('renders an empty-state chip when a model is selected but no usage event has fired yet', () => {
    // Phase 3 — TUI-cadence parity. The chip is now permanently
    // visible whenever a model is selected, mirroring the TUI's
    // StatusBar (always visible, reflects current state). Empty
    // state shows a placeholder text without numeric figures so the
    // user sees the affordance is present and waiting for data
    // rather than concluding it is broken.
    const store = useChatStore()
    store.currentModelId = 'glm-4.6'
    store.currentContextUsage = null

    const wrapper = mount(ContextUsageChip)

    const chip = wrapper.find('[data-testid="context-usage-chip"]')
    expect(chip.exists()).toBe(true)
    expect(chip.attributes('data-severity')).toBe('neutral')
    // Empty-state copy: dashes for both counts so "0/0" doesn't
    // miscommunicate "saturated zero-budget context".
    expect(wrapper.find('[data-testid="context-usage-counts"]').text()).toBe('—/—')
    expect(wrapper.find('[data-testid="context-usage-percentage"]').text()).toBe('—%')
  })

  it('does not render when no model is selected', () => {
    // No-model edge: at very early bootstrap the user has not
    // picked a model and the session has no current model. Hide
    // the chip rather than show a model-less affordance.
    const store = useChatStore()
    store.currentModelId = ''
    store.currentContextUsage = null

    const wrapper = mount(ContextUsageChip)

    expect(wrapper.find('[data-testid="context-usage-chip"]').exists()).toBe(false)
  })

  it('falls back to empty-state when a degraded payload arrives with limit=0', () => {
    // A zero-limit figure would render `1234/0` which is meaningless.
    // The engine suppresses the chunk when limit<=0 so this should
    // never reach the chip in practice, but the chip guards against
    // a future emitter regression. With Phase 3's always-visible
    // behaviour we no longer hide the chip — instead the figure
    // falls back to the empty-state placeholder so the affordance
    // remains present without misleading the user.
    const store = useChatStore()
    store.currentModelId = 'glm-4.6'
    store.currentContextUsage = {
      inputTokens: 1234,
      outputReserve: 4096,
      limit: 0,
      percentage: 0,
    }

    const wrapper = mount(ContextUsageChip)

    const chip = wrapper.find('[data-testid="context-usage-chip"]')
    expect(chip.exists()).toBe(true)
    expect(wrapper.find('[data-testid="context-usage-counts"]').text()).toBe('—/—')
  })

  it('renders the chip with input/limit and percentage when currentContextUsage is set', () => {
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 12345,
      outputReserve: 4096,
      limit: 100000,
      percentage: 12,
    }

    const wrapper = mount(ContextUsageChip)

    const chip = wrapper.find('[data-testid="context-usage-chip"]')
    expect(chip.exists()).toBe(true)
    // 12345 → 12K, 100000 → 100K
    expect(wrapper.find('[data-testid="context-usage-counts"]').text()).toBe('12K/100K')
    expect(wrapper.find('[data-testid="context-usage-percentage"]').text()).toBe('12%')
  })

  it('formats sub-1000 token counts verbatim', () => {
    // The compact 'K' formatter applies only above 1000. A small
    // session shows `42/100K` rather than `0K/100K` which would
    // mis-suggest a saturated state.
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 42,
      outputReserve: 4096,
      limit: 100000,
      percentage: 0,
    }

    const wrapper = mount(ContextUsageChip)

    expect(wrapper.find('[data-testid="context-usage-counts"]').text()).toBe('42/100K')
  })

  it('exposes role="status" with aria-live="polite" for screen-reader announcement', () => {
    // Informational severity — the chip updates per turn and the
    // user reads it at their own pace. role="status" + polite is the
    // appropriate pairing (assertive is reserved for the
    // CriticalErrorBanner which announces an unrecoverable session
    // state).
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 1000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 1,
    }

    const wrapper = mount(ContextUsageChip)

    const chip = wrapper.find('[data-testid="context-usage-chip"]')
    expect(chip.attributes('role')).toBe('status')
    expect(chip.attributes('aria-live')).toBe('polite')
  })

  it('classes the chip as "neutral" below the 75% threshold', () => {
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 50000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 50,
    }

    const wrapper = mount(ContextUsageChip)

    const chip = wrapper.find('[data-testid="context-usage-chip"]')
    expect(chip.attributes('data-severity')).toBe('neutral')
    expect(chip.classes()).toContain('context-usage-chip--neutral')
  })

  it('classes the chip as "warning" between 75% and 89%', () => {
    // 75% is the threshold where the conversation enters
    // "approaching saturation" territory. The chip pivots to amber
    // so the user sees the warning before the danger threshold.
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 80000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 80,
    }

    const wrapper = mount(ContextUsageChip)

    const chip = wrapper.find('[data-testid="context-usage-chip"]')
    expect(chip.attributes('data-severity')).toBe('warning')
    expect(chip.classes()).toContain('context-usage-chip--warning')
  })

  it('classes the chip as "danger" at or above 90%', () => {
    // 90%+ is "compact or fail next turn" territory. The chip pivots
    // to the same red severity as the CriticalErrorBanner so the
    // visual escalation across the chat surface is consistent.
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 95000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 95,
    }

    const wrapper = mount(ContextUsageChip)

    const chip = wrapper.find('[data-testid="context-usage-chip"]')
    expect(chip.attributes('data-severity')).toBe('danger')
    expect(chip.classes()).toContain('context-usage-chip--danger')
  })

  // ---- Slice 6b — auto-compaction flash + tooltip ---------------------
  //
  // The Go SSE pipeline emits a `context_compacted` event when the L2
  // auto-compactor's gate-proximity force-fire (Slice 6a) summarises a
  // cold prefix. The chat store's handleContextCompactedEvent records
  // the payload onto `lastCompaction` and increments
  // `compactionEventCount`; the chip observes both and (a) flashes a
  // 2-second visual acknowledgement on each new event, (b) exposes a
  // hover tooltip carrying the saved-tokens delta whenever a compaction
  // has fired this session.
  //
  // The chip's underlying severity figure keeps tracking the next
  // `context_usage` event in parallel — the flash is purely a transient
  // overlay that does NOT replace the live usage palette.

  it('does not render the compaction flash when no event has fired (Slice 6b)', () => {
    // Pristine state: a session that has not yet seen any compaction
    // shows the chip without the flash overlay. The flash only ever
    // appears in response to an event.
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 1000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 1,
    }
    store.compactionEventCount = 0
    store.lastCompaction = null

    const wrapper = mount(ContextUsageChip)

    expect(wrapper.find('[data-component="context-compacted-flash"]').exists()).toBe(false)
    // The chip's title attribute carries the tooltip; without a
    // compaction the title must be absent (or empty) so the
    // browser does not render a misleading native tooltip.
    const chip = wrapper.find('[data-testid="context-usage-chip"]')
    const title = chip.attributes('title') ?? ''
    expect(title).toBe('')
  })

  it('renders the compaction flash for ~2s after a compaction event then auto-clears (Slice 6b)', async () => {
    // Fake-timer pin on the flash duration. The chip watcher sets a
    // `flashing` ref true, schedules a 2000ms setTimeout, then clears
    // it. The overlay element gates on `flashing.value === true`.
    vi.useFakeTimers()
    try {
      const store = useChatStore()
      store.currentContextUsage = {
        inputTokens: 1000,
        outputReserve: 4096,
        limit: 100000,
        percentage: 1,
      }
      store.compactionEventCount = 0
      store.lastCompaction = null

      const wrapper = mount(ContextUsageChip)
      // No flash yet.
      expect(wrapper.find('[data-component="context-compacted-flash"]').exists()).toBe(false)

      // Simulate a compaction event landing — the store's handler is
      // the canonical path but the chip's watcher only cares about
      // the reactive state changing, so we mutate it directly.
      store.compactionEventCount = 1
      store.lastCompaction = {
        originalTokens: 50000,
        summaryTokens: 5000,
        tokensSaved: 45000,
        at: Date.now(),
        trigger: '',
      }
      await wrapper.vm.$nextTick()

      // Mid-flash (1s into the 2s window) the overlay is present.
      vi.advanceTimersByTime(1000)
      await wrapper.vm.$nextTick()
      expect(wrapper.find('[data-component="context-compacted-flash"]').exists()).toBe(true)

      // Past the 2s window the overlay clears itself.
      vi.advanceTimersByTime(1500)
      await wrapper.vm.$nextTick()
      expect(wrapper.find('[data-component="context-compacted-flash"]').exists()).toBe(false)
    } finally {
      vi.useRealTimers()
    }
  })

  it('renders a hover tooltip with the saved-tokens delta after compaction (Slice 6b)', () => {
    // The chip's `title` attribute is the project's tooltip convention
    // (see MessageBubble.vue's "Message failed to send" / "Revert to
    // this message" title attrs). After a compaction event the title
    // carries the saved-tokens delta in the same compact `K` formatter
    // the chip uses for its primary figures (50000 → 50K).
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 5000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 5,
    }
    store.compactionEventCount = 1
    store.lastCompaction = {
      originalTokens: 50000,
      summaryTokens: 5000,
      tokensSaved: 45000,
      at: Date.now(),
      trigger: '',
    }

    const wrapper = mount(ContextUsageChip)

    const chip = wrapper.find('[data-testid="context-usage-chip"]')
    const title = chip.attributes('title') ?? ''
    expect(title).toContain('saved 45K tokens')
    // The tooltip also surfaces the before → after pair so an operator
    // can sanity-check the compactor's aggressiveness from the chip
    // itself without opening a session-recording.
    expect(title).toContain('50K')
    expect(title).toContain('5K')
  })

  // ---- Phase-5 Slice δ — Trigger discriminant in chip tooltip --------
  //
  // The compaction trigger discriminant lands on `lastCompaction.trigger`
  // (set by the chat store). The chip tooltip reads it and surfaces a
  // human-readable phrase distinguishing the four causes:
  //
  //   - "ratio"             → "compacted on threshold"
  //   - "gate_proximity"    → "compacted near limit"
  //   - "model_switch"      → "compacted on model switch"
  //   - "tool_result_wave"  → "compacted after tool result"
  //
  // Empty / unknown triggers fall back to the generic copy so historical
  // events that pre-date the field remain decodable.

  it('surfaces "compacted on threshold" copy for trigger=ratio (Slice δ)', () => {
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 5000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 5,
    }
    store.compactionEventCount = 1
    store.lastCompaction = {
      originalTokens: 50000,
      summaryTokens: 5000,
      tokensSaved: 45000,
      at: Date.now(),
      trigger: 'ratio',
    }

    const wrapper = mount(ContextUsageChip)

    const title = wrapper.find('[data-testid="context-usage-chip"]').attributes('title') ?? ''
    expect(title).toContain('compacted on threshold')
  })

  it('surfaces "compacted near limit" copy for trigger=gate_proximity (Slice δ)', () => {
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 95000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 95,
    }
    store.compactionEventCount = 1
    store.lastCompaction = {
      originalTokens: 50000,
      summaryTokens: 5000,
      tokensSaved: 45000,
      at: Date.now(),
      trigger: 'gate_proximity',
    }

    const wrapper = mount(ContextUsageChip)

    const title = wrapper.find('[data-testid="context-usage-chip"]').attributes('title') ?? ''
    expect(title).toContain('compacted near limit')
  })

  it('surfaces "compacted on model switch" copy for trigger=model_switch (Slice δ)', () => {
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 5000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 5,
    }
    store.compactionEventCount = 1
    store.lastCompaction = {
      originalTokens: 50000,
      summaryTokens: 5000,
      tokensSaved: 45000,
      at: Date.now(),
      trigger: 'model_switch',
    }

    const wrapper = mount(ContextUsageChip)

    const title = wrapper.find('[data-testid="context-usage-chip"]').attributes('title') ?? ''
    expect(title).toContain('compacted on model switch')
  })

  it('surfaces "compacted after tool result" copy for trigger=tool_result_wave (Slice δ)', () => {
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 5000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 5,
    }
    store.compactionEventCount = 1
    store.lastCompaction = {
      originalTokens: 50000,
      summaryTokens: 5000,
      tokensSaved: 45000,
      at: Date.now(),
      trigger: 'tool_result_wave',
    }

    const wrapper = mount(ContextUsageChip)

    const title = wrapper.find('[data-testid="context-usage-chip"]').attributes('title') ?? ''
    expect(title).toContain('compacted after tool result')
  })

  it('falls back to the generic copy when trigger is empty or unknown (Slice δ)', () => {
    // Defence in depth: a historical event that pre-dates the field
    // (or a future emitter that ships an unrecognised value) keeps the
    // saved-tokens copy without surfacing a misattribution.
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 5000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 5,
    }
    store.compactionEventCount = 1
    store.lastCompaction = {
      originalTokens: 50000,
      summaryTokens: 5000,
      tokensSaved: 45000,
      at: Date.now(),
      trigger: '',
    }

    const wrapper = mount(ContextUsageChip)

    const title = wrapper.find('[data-testid="context-usage-chip"]').attributes('title') ?? ''
    expect(title).toContain('saved 45K tokens')
    // No misattribution for empty trigger.
    expect(title).not.toContain('compacted on threshold')
    expect(title).not.toContain('compacted near limit')
    expect(title).not.toContain('compacted on model switch')
    expect(title).not.toContain('compacted after tool result')
  })

  it('preserves the existing severity colours during a compaction flash (Slice 6b)', async () => {
    // High-usage state combined with a fresh compaction event: the
    // chip's severity (data-severity / context-usage-chip--danger)
    // must stay intact while the flash overlay sits on top. Without
    // this guard a future implementation that swapped the chip class
    // during the flash would mask the danger palette and visually
    // imply the compaction succeeded in returning the chip to neutral
    // — which is misleading because the figure that next arrives is
    // what the operator should be reading, not the pre-compaction
    // figure paired with a "compacted" overlay.
    vi.useFakeTimers()
    try {
      const store = useChatStore()
      store.currentContextUsage = {
        inputTokens: 95000,
        outputReserve: 4096,
        limit: 100000,
        percentage: 95,
      }

      const wrapper = mount(ContextUsageChip)

      // Fire the compaction event — flash triggers but danger stays.
      store.compactionEventCount = 1
      store.lastCompaction = {
        originalTokens: 50000,
        summaryTokens: 5000,
        tokensSaved: 45000,
        at: Date.now(),
        trigger: '',
      }
      await wrapper.vm.$nextTick()

      const chip = wrapper.find('[data-testid="context-usage-chip"]')
      expect(chip.attributes('data-severity')).toBe('danger')
      expect(chip.classes()).toContain('context-usage-chip--danger')
      expect(wrapper.find('[data-component="context-compacted-flash"]').exists()).toBe(true)
    } finally {
      vi.useRealTimers()
    }
  })

  // N8 (Vue UI Parity vs OpenCode, May 2026): severity colours via
  // theme variables, not hardcoded rgb(). The chip's scoped CSS used
  // `rgb(220, 38, 38)` (danger) and `rgb(217, 119, 6)` (warning) inline
  // which prevents the colours from re-skinning under `[data-theme]`
  // swaps. The fix swaps to `var(--error)` / `var(--warning)` so the
  // chip palette tracks the active theme.
  //
  // The scoped CSS lives in <style scoped> — testing it via the
  // component's CSS text rather than computed-style (jsdom does not
  // resolve CSS variables to colours; computed style returns the raw
  // var() expression).
  it('declares severity colours via CSS theme variables (var(--error) / var(--warning)) — N8', async () => {
    // Component CSS ships as part of the source file; we read it from
    // the scoped SFC to pin the post-fix state. The pre-fix CSS held
    // the literal hex/rgb strings — that's the regression we're
    // guarding against.
    const componentSource = await import(
      './ContextUsageChip.vue?raw'
    )
    const css = componentSource.default
    // Post-fix: theme vars present.
    expect(css).toMatch(/var\(--error\b/)
    expect(css).toMatch(/var\(--warning\b/)
    // Pre-fix: hardcoded literal rgb() colours absent.
    expect(css).not.toMatch(/rgb\(\s*220\s*,\s*38\s*,\s*38\s*\)/)
    expect(css).not.toMatch(/rgb\(\s*217\s*,\s*119\s*,\s*6\s*\)/)
  })

  it('reactively updates the rendered figure when the store slice changes', async () => {
    // The chip subscribes to the store's reactive slice so successive
    // turns update the figure in place. A new context_usage event for
    // the same session should show new figures without remount.
    const store = useChatStore()
    store.currentContextUsage = {
      inputTokens: 1000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 1,
    }

    const wrapper = mount(ContextUsageChip)
    expect(wrapper.find('[data-testid="context-usage-percentage"]').text()).toBe('1%')

    store.currentContextUsage = {
      inputTokens: 80000,
      outputReserve: 4096,
      limit: 100000,
      percentage: 80,
    }
    await wrapper.vm.$nextTick()

    expect(wrapper.find('[data-testid="context-usage-percentage"]').text()).toBe('80%')
    expect(wrapper.find('[data-testid="context-usage-chip"]').attributes('data-severity')).toBe(
      'warning',
    )
  })
})
