import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import App from './App.vue'
import { useChatStore } from '@/stores/chatStore'

// App-level test surface — pins the loading-overlay contract that gates
// first-paint of the application until bootstrap (health-check + the
// chatStore.bootstrap() singleton) has settled.
//
// Why App is the right home for this test:
//   - The overlay must cover the FOUC window between Vue mount and "user
//     can interact". App.vue is the only component that owns that window.
//   - The router-view (and therefore every per-route bootstrap eg.
//     ChatView's restoreStateFromBackend) is gated on the same readiness
//     signal. Pinning the gate at App-level is the only place where the
//     contract is observable end-to-end.
//   - The HTML splash in index.html cannot be exercised by jsdom (no Vite
//     pipeline), so the visual cover gets verified separately via
//     Playwright in dev. This spec covers the Vue-component half.

vi.mock('@/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api')>()
  return {
    ...actual,
    fetchModels: vi.fn().mockResolvedValue([]),
    listModels: vi.fn().mockResolvedValue({ providers: [] }),
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessions: vi.fn().mockResolvedValue([]),
  }
})

// Stub vue-router so App.vue's <RouterView /> resolves without a real
// router; we only care about whether RouterView is mounted, not what it
// renders. mount() global.stubs below also covers the template-side
// auto-resolution that the router plugin would normally provide.
vi.mock('vue-router', () => ({
  RouterView: { name: 'RouterView', template: '<div data-testid="router-view-stub"></div>' },
  useRouter: () => ({ push: vi.fn() }),
  useRoute: () => ({ path: '/chat' }),
}))

const mountOptions = {
  global: {
    stubs: {
      RouterView: { name: 'RouterView', template: '<div data-testid="router-view-stub"></div>' },
    },
  },
}

// Stub NavBar — it does its own picker hydration on mount, which would
// pull in agent / model / session machinery we are not exercising here.
vi.mock('@/components/layout/NavBar.vue', () => ({
  default: { name: 'NavBar', template: '<div data-testid="nav-bar-stub"></div>' },
}))

// Stub ToastContainer — pure-presentation, irrelevant to the overlay
// gate, drags in useToast composable singleton state we do not want.
vi.mock('@/components/common/ToastContainer.vue', () => ({
  default: { name: 'ToastContainer', template: '<div data-testid="toast-stub"></div>' },
}))

describe('App loading overlay', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    // Default: a healthy backend so the health-check resolves quickly.
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({ ok: true } as Response),
    )
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders the loading overlay on initial mount', async () => {
    // Hold bootstrap in flight indefinitely so we can observe the
    // pre-ready state of the DOM.
    const chatStore = useChatStore()
    vi.spyOn(chatStore, 'bootstrap').mockReturnValue(new Promise(() => {}))

    const wrapper = mount(App, mountOptions)

    expect(wrapper.find('[data-testid="app-loading-overlay"]').exists()).toBe(true)
  })

  it('hides the router view (page content) while the overlay is visible', async () => {
    const chatStore = useChatStore()
    vi.spyOn(chatStore, 'bootstrap').mockReturnValue(new Promise(() => {}))

    const wrapper = mount(App, mountOptions)

    // The page underneath must NOT be visible during loading. We assert
    // both that the RouterView host is absent from the rendered DOM AND
    // that the overlay is present — together they prove no half-built
    // page leaks under the overlay.
    expect(wrapper.find('[data-testid="router-view-stub"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="app-loading-overlay"]').exists()).toBe(true)
  })

  it('dismisses the overlay and renders the router view once bootstrap resolves', async () => {
    const chatStore = useChatStore()
    vi.spyOn(chatStore, 'bootstrap').mockResolvedValue(undefined)

    const wrapper = mount(App, mountOptions)
    await flushPromises()

    expect(wrapper.find('[data-testid="app-loading-overlay"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="router-view-stub"]').exists()).toBe(true)
  })

  it('still dismisses the overlay when bootstrap rejects (so a network blip does not strand the user behind a permanent splash)', async () => {
    const chatStore = useChatStore()
    vi.spyOn(chatStore, 'bootstrap').mockRejectedValue(new Error('network down'))

    const wrapper = mount(App, mountOptions)
    await flushPromises()

    expect(wrapper.find('[data-testid="app-loading-overlay"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="router-view-stub"]').exists()).toBe(true)
  })

  it('still dismisses the overlay when the health-check rejects', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockRejectedValue(new Error('health-check failed')),
    )
    const chatStore = useChatStore()
    vi.spyOn(chatStore, 'bootstrap').mockResolvedValue(undefined)

    const wrapper = mount(App, mountOptions)
    await flushPromises()

    expect(wrapper.find('[data-testid="app-loading-overlay"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="router-view-stub"]').exists()).toBe(true)
  })

  it('removes the index.html splash element on mount so the Vue overlay can take over without double-stacking', async () => {
    // index.html ships an HTML-level splash with id="app-loading-splash"
    // to cover the FOUC window before Vue mounts. Once App.vue mounts,
    // its own LoadingOverlay supersedes that element — leaving both in
    // the DOM would stack two opaque covers on top of one another.
    const splash = document.createElement('div')
    splash.id = 'app-loading-splash'
    document.body.appendChild(splash)

    const chatStore = useChatStore()
    vi.spyOn(chatStore, 'bootstrap').mockReturnValue(new Promise(() => {}))

    mount(App, mountOptions)

    expect(document.getElementById('app-loading-splash')).toBeNull()
  })
})
