/**
 * LoginView.spec.ts — Vitest component spec for the FlowState API
 * Auth Track PR3/C8 login surface.
 *
 * Pins:
 *   - The form renders username + password fields and a collapsible
 *     "deployment secret" details section (mode-agnostic shape — see
 *     LoginView.vue for the B8-discipline rationale).
 *   - Submit button is disabled until at least one credential shape
 *     is filled.
 *   - POST /auth/login is called with `credentials: 'include'` AND a
 *     JSON body shaped by whichever fields the user filled in. The
 *     fetch shape matches the server's parseCredentials expectations.
 *   - On 401 the toast surface shows "Invalid credentials" — uniform,
 *     no mode-fingerprint leak.
 *   - On 200 router.push('/chat') fires.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import LoginView from './LoginView.vue'

function makeRouter() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/login', component: LoginView, name: 'login' },
      { path: '/chat', component: { template: '<div>chat</div>' }, name: 'chat' },
    ],
  })
  return router
}

async function mountWithRouter() {
  const router = makeRouter()
  await router.push('/login')
  await router.isReady()
  const wrapper = mount(LoginView, {
    global: { plugins: [router] },
  })
  return { wrapper, router }
}

// Toast mock — useToast.ts exports `showToast` as a module-level
// function. We mock the module so the spec can assert on the calls
// without driving Vue's reactivity.
vi.mock('@/composables/useToast', () => ({
  showToast: vi.fn(),
}))

describe('LoginView', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.clearAllMocks()
  })

  it('renders username + password fields and a collapsible secret section', async () => {
    const { wrapper } = await mountWithRouter()
    expect(wrapper.find('[data-testid="login-username"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="login-password"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="login-secret-section"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="login-secret"]').exists()).toBe(true)
  })

  it('disables the submit button when no fields are filled', async () => {
    const { wrapper } = await mountWithRouter()
    const btn = wrapper.find('[data-testid="login-submit"]')
    expect((btn.element as HTMLButtonElement).disabled).toBe(true)
  })

  it('enables submit once username + password are filled', async () => {
    const { wrapper } = await mountWithRouter()
    await wrapper.find('[data-testid="login-username"]').setValue('alice')
    await wrapper.find('[data-testid="login-password"]').setValue('secret')
    const btn = wrapper.find('[data-testid="login-submit"]')
    expect((btn.element as HTMLButtonElement).disabled).toBe(false)
  })

  it('enables submit once secret alone is filled', async () => {
    const { wrapper } = await mountWithRouter()
    await wrapper.find('[data-testid="login-secret"]').setValue('shared-secret-value')
    const btn = wrapper.find('[data-testid="login-submit"]')
    expect((btn.element as HTMLButtonElement).disabled).toBe(false)
  })

  it('POSTs to /api/auth/login with credentials: include on submit', async () => {
    // memory feedback_response_ok_mock_gotcha — mock must produce a
    // Response with `ok` getter, not a bare `{status: 200}` literal.
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ csrf_token: 'abc' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { wrapper } = await mountWithRouter()
    await wrapper.find('[data-testid="login-username"]').setValue('alice')
    await wrapper.find('[data-testid="login-password"]').setValue('p4ss')
    await wrapper.find('[data-testid="login-form"]').trigger('submit')
    await flushPromises()

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, init] = fetchMock.mock.calls[0]
    expect(String(url)).toContain('/api/auth/login')
    expect((init as RequestInit).method).toBe('POST')
    expect((init as RequestInit).credentials).toBe('include')
    const body = JSON.parse(String((init as RequestInit).body))
    expect(body.username).toBe('alice')
    expect(body.password).toBe('p4ss')
  })

  it('routes to /chat on 200 login response', async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ csrf_token: 'abc' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { wrapper, router } = await mountWithRouter()
    await wrapper.find('[data-testid="login-secret"]').setValue('shared-secret')
    await wrapper.find('[data-testid="login-form"]').trigger('submit')
    await flushPromises()

    expect(router.currentRoute.value.path).toBe('/chat')
  })

  it('shows "Invalid credentials" toast on 401', async () => {
    const { showToast } = await import('@/composables/useToast')
    // ok=false comes from status>=400; mock must use real Response to
    // get the `ok` getter (memory feedback_response_ok_mock_gotcha).
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: 'invalid_credentials' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    const { wrapper, router } = await mountWithRouter()
    await wrapper.find('[data-testid="login-secret"]').setValue('wrong')
    await wrapper.find('[data-testid="login-form"]').trigger('submit')
    await flushPromises()

    expect(showToast).toHaveBeenCalledWith(
      expect.objectContaining({
        message: 'Invalid credentials',
        variant: 'error',
      }),
    )
    // Stays on /login on failure
    expect(router.currentRoute.value.path).toBe('/login')
  })

  it('shows a generic error toast on network failure', async () => {
    const { showToast } = await import('@/composables/useToast')
    fetchMock.mockRejectedValueOnce(new Error('network down'))

    // Silence the expected console.error so the test output stays
    // clean.
    const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {})

    const { wrapper } = await mountWithRouter()
    await wrapper.find('[data-testid="login-secret"]').setValue('any')
    await wrapper.find('[data-testid="login-form"]').trigger('submit')
    await flushPromises()

    expect(showToast).toHaveBeenCalledWith(
      expect.objectContaining({
        message: expect.stringContaining('Could not reach the server'),
        variant: 'error',
      }),
    )
    consoleSpy.mockRestore()
  })
})
