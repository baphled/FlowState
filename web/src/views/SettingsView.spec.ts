import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import SettingsView from './SettingsView.vue'

const localStorageMock = (() => {
  let store: Record<string, string> = {}
  return {
    getItem: vi.fn((key: string) => store[key] ?? null),
    setItem: vi.fn((key: string, value: string) => {
      store[key] = value
    }),
    removeItem: vi.fn((key: string) => {
      delete store[key]
    }),
    clear: vi.fn(() => {
      store = {}
    }),
  }
})()

Object.defineProperty(window, 'localStorage', { value: localStorageMock })

vi.mock('@/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api')>()
  return {
    ...actual,
    fetchCompressionConfig: vi.fn().mockResolvedValue({ threshold: 0.75 }),
    updateCompressionThreshold: vi.fn().mockImplementation((t: number) =>
      Promise.resolve({ threshold: t }),
    ),
  }
})

// Deliverable 2 (May 2026 context-accuracy bundle) — Settings UI
// surface for the runtime-tunable auto-compaction threshold. The
// SettingsView hydrates the slider from GET /api/v1/config/compression
// on mount and commits new values via PATCH on change.
describe('SettingsView - compression threshold control', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorageMock.clear()
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  it('renders a compression section once the backend reports a configured threshold', async () => {
    const wrapper = mount(SettingsView)
    await flushPromises()

    expect(wrapper.find('[data-testid="compression-section"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="compression-threshold-input"]').exists()).toBe(true)
  })

  it('hydrates the threshold input from the backend response', async () => {
    const { fetchCompressionConfig } = await import('@/api')
    vi.mocked(fetchCompressionConfig).mockResolvedValueOnce({ threshold: 0.42 })

    const wrapper = mount(SettingsView)
    await flushPromises()

    const input = wrapper.find('[data-testid="compression-threshold-input"]').element as HTMLInputElement
    // The input binds the value as a fraction; the slider displays
    // percentage but the underlying number is the (0, 1] ratio.
    expect(parseFloat(input.value)).toBeCloseTo(0.42, 6)
  })

  it('PATCHes the new threshold when the slider commits', async () => {
    const { updateCompressionThreshold } = await import('@/api')

    const wrapper = mount(SettingsView)
    await flushPromises()

    const input = wrapper.find('[data-testid="compression-threshold-input"]')
    await input.setValue('0.55')
    await input.trigger('change')
    await flushPromises()

    expect(vi.mocked(updateCompressionThreshold)).toHaveBeenCalledWith(0.55)
  })

  it('hides the compression section when the backend reports no controller (null config)', async () => {
    const { fetchCompressionConfig } = await import('@/api')
    vi.mocked(fetchCompressionConfig).mockResolvedValueOnce(null)

    const wrapper = mount(SettingsView)
    await flushPromises()

    // Compression section must not render when the feature is
    // unavailable on this deployment — operators should not see a
    // control that doesn't function.
    expect(wrapper.find('[data-testid="compression-section"]').exists()).toBe(false)
  })
})

// N2 + N1 (Vue UI Parity vs OpenCode, May 2026) — additional theme
// palettes + hover preview on theme picker.
describe('SettingsView - theme picker (N2 + N1)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorageMock.clear()
    // Reset <html data-theme> so each spec asserts a clean baseline.
    document.documentElement.removeAttribute('data-theme')
  })

  afterEach(() => {
    vi.clearAllMocks()
    document.documentElement.removeAttribute('data-theme')
  })

  // N2 — ship additional community-flavoured palettes. Each option in
  // the picker must (a) render, (b) be clickable, (c) commit the value
  // to the settings store.
  it('renders all additional theme options (N2)', async () => {
    const wrapper = mount(SettingsView)
    await flushPromises()

    // Pre-existing baseline themes.
    expect(wrapper.find('[data-testid="theme-option-dark"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="theme-option-light"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="theme-option-terminal"]').exists()).toBe(true)

    // N2 additions.
    expect(wrapper.find('[data-testid="theme-option-tokyo-night"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="theme-option-catppuccin-mocha"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="theme-option-dracula"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="theme-option-nord"]').exists()).toBe(true)
  })

  it('applies <html data-theme> when a new theme is selected (N2)', async () => {
    const wrapper = mount(SettingsView)
    await flushPromises()

    const radio = wrapper.find(
      '[data-testid="theme-option-tokyo-night"] input[type="radio"]',
    )
    expect(radio.exists()).toBe(true)
    await radio.setValue(true)
    await flushPromises()

    expect(document.documentElement.getAttribute('data-theme')).toBe('tokyo-night')
  })

  // N1 — live preview on hover. The user mouse-enters a theme option
  // and the document gets `data-theme=<that-option>` while pointing;
  // mouse-leave reverts to the currently-selected theme.
  it('previews a theme on pointer enter and reverts on leave (N1)', async () => {
    const wrapper = mount(SettingsView, { attachTo: document.body })
    await flushPromises()
    // Baseline: settings store sits at 'dark' (the readTheme default
    // when localStorage is empty). The store applies it via
    // applyTheme() inside setTheme — but the constructor does NOT
    // call applyTheme. Seed the baseline explicitly so the revert
    // target is unambiguous.
    document.documentElement.setAttribute('data-theme', 'dark')

    const option = wrapper.find('[data-testid="theme-option-tokyo-night"]')
    await option.trigger('mouseenter')
    expect(document.documentElement.getAttribute('data-theme')).toBe('tokyo-night')

    await option.trigger('mouseleave')
    // Revert to the active selection — 'dark' baseline.
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark')

    wrapper.unmount()
  })

  it('does not override an explicit selection on mouseleave (N1)', async () => {
    const wrapper = mount(SettingsView, { attachTo: document.body })
    await flushPromises()
    document.documentElement.setAttribute('data-theme', 'dark')

    // User commits to Nord.
    const nordRadio = wrapper.find('[data-testid="theme-option-nord"] input[type="radio"]')
    await nordRadio.setValue(true)
    await flushPromises()
    expect(document.documentElement.getAttribute('data-theme')).toBe('nord')

    // Then hovers Dracula, leaves.
    const dracula = wrapper.find('[data-testid="theme-option-dracula"]')
    await dracula.trigger('mouseenter')
    expect(document.documentElement.getAttribute('data-theme')).toBe('dracula')

    await dracula.trigger('mouseleave')
    // Revert to the active selection — Nord, not the pre-Nord 'dark'.
    expect(document.documentElement.getAttribute('data-theme')).toBe('nord')

    wrapper.unmount()
  })
})
