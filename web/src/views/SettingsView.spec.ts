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
