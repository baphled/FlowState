<template>
  <!--
    PR3 / C8 — LoginView. The login surface for the FlowState API Auth
    Track. Flag-gated server-side at PR3/C7 (features.auth_v1); when the
    flag is off, this view is reachable at `/login` but submitting the
    form returns whatever the un-wrapped POST /api/auth/login handler
    returns (or 404 if the route is not yet wired at the cmd/serve
    layer). When PR5 flips the flag default-on, the same view ships
    user-visible.

    Mode handling: per plan B8 (§"Wire Protocol" line 482), the server
    returns a uniform 401 invalid_credentials on every shape mismatch
    so probers cannot fingerprint auth.mode. We mirror that discipline
    on the UI side: render BOTH the secret field AND the
    username/password fields; on submit, send whatever the user filled
    in. The server's parseCredentials picks the right struct from its
    configured mode; the SPA stays mode-agnostic. Simpler than querying
    /api/auth/whoami at mount, and impossible to drift between the
    SPA's mode-detection and the server's actual mode.

    No /api/auth/mode endpoint is queried — the plan §"Endpoint
    Inventory" line 400 calls for /api/auth/whoami, but the simpler
    "render both shapes" approach is documented in the C8 brief and
    matches B8's wire discipline.
  -->
  <div class="login-view" data-testid="login-view">
    <div class="login-card">
      <h1>Sign in to FlowState</h1>
      <p class="login-subtitle">
        Enter your credentials to continue.
      </p>

      <form @submit.prevent="onSubmit" data-testid="login-form">
        <!--
          Secret field — used by `shared-secret` and
          `per-deployment-login` modes. Hidden behind <details>
          (collapsed by default) when username+password is the more
          common deployment shape. Both inputs visible to keep the form
          mode-agnostic.
        -->
        <label class="field-label" for="login-username">Username</label>
        <input
          id="login-username"
          v-model="username"
          class="field-input"
          data-testid="login-username"
          type="text"
          autocomplete="username"
          :disabled="submitting"
        />

        <label class="field-label" for="login-password">Password</label>
        <input
          id="login-password"
          v-model="password"
          class="field-input"
          data-testid="login-password"
          type="password"
          autocomplete="current-password"
          :disabled="submitting"
        />

        <details class="login-secret-section" data-testid="login-secret-section">
          <summary>Deployment secret (alternative)</summary>
          <label class="field-label" for="login-secret">Shared secret</label>
          <input
            id="login-secret"
            v-model="secret"
            class="field-input"
            data-testid="login-secret"
            type="password"
            autocomplete="current-password"
            :disabled="submitting"
          />
          <p class="field-hint">
            Required for `shared-secret` / `per-deployment-login`
            deployment modes. Leave blank if your deployment uses
            multi-user mode.
          </p>
        </details>

        <button
          type="submit"
          class="login-submit"
          data-testid="login-submit"
          :disabled="submitting || !canSubmit"
        >
          {{ submitting ? 'Signing in…' : 'Sign in' }}
        </button>
      </form>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { joinBaseURL } from '@/api'
import { showToast } from '@/composables/useToast'
import { ensureCsrfToken } from '@/lib/csrf'
import { useCsrfStore } from '@/stores/csrfStore'

// State — three free-form fields. The form renders all three; the
// server's mode dictates which actually mints a session. The UI
// surfaces ONLY a uniform "Invalid credentials" on failure (no mode
// hint) to preserve the server's B8 wire discipline on the
// rendered-surface side too.
const username = ref('')
const password = ref('')
const secret = ref('')
const submitting = ref(false)
const router = useRouter()

// canSubmit: enable the button when at least ONE of the credential
// shapes is filled. The deeper validation lives on the server side; we
// just stop empty-form submissions from generating a needless 401.
const canSubmit = computed(() => {
  return (
    secret.value.length > 0 ||
    (username.value.length > 0 && password.value.length > 0)
  )
})

async function onSubmit() {
  if (!canSubmit.value) return
  submitting.value = true

  // Build the request body. The server's parseCredentials (in
  // internal/auth/login.go) reads the field shape that matches its
  // configured auth.mode and ignores the others (silent extra-field
  // drop, per B8 fold). The SPA sends all three when populated; the
  // server picks the right one.
  const body: Record<string, string> = {}
  if (secret.value) body.secret = secret.value
  if (username.value) body.username = username.value
  if (password.value) body.password = password.value

  try {
    // QA BUG-1/BUG-2 fix (May 2026): prefetch the masked CSRF token
    // before submitting. Without this, the first-time login flow has
    // no _csrf cookie and gorilla/csrf rejects the POST with 403
    // before credentials are evaluated. ensureCsrfToken hits
    // GET /api/auth/csrf, which routes through the LoginChain wrap so
    // the server issues the _csrf cookie + returns the matching
    // masked token. The token is cached in the Pinia csrfStore; the
    // X-CSRF-Token header below reads it via getCsrfToken().
    let csrfToken: string
    try {
      csrfToken = await ensureCsrfToken()
    } catch (err) {
      // Pre-flight prefetch failed (network, server misconfig). Surface
      // a clear error rather than firing the POST without a token — the
      // user gets a "could not reach the server" toast and the operator
      // sees the prefetch failure in the browser network panel.
      showToast({
        message: 'Could not reach the server. Try again.',
        variant: 'error',
      })
      // eslint-disable-next-line no-console
      console.error('[flowstate] csrf prefetch failed:', err)
      return
    }

    const res = await fetch(joinBaseURL('/auth/login'), {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-CSRF-Token': csrfToken,
      },
      credentials: 'include',
      body: JSON.stringify(body),
    })

    if (res.status === 401) {
      // Uniform error per plan B8. No mode-specific hint.
      showToast({ message: 'Invalid credentials', variant: 'error' })
      return
    }
    if (!res.ok) {
      showToast({
        message: `Login failed (${res.status})`,
        variant: 'error',
      })
      return
    }

    // 200 — cookie set by Set-Cookie; capture the rotated csrf_token
    // from the response body so subsequent authenticated requests use
    // the post-login token (which is bound to the new session Record
    // by the server-side RequireCSRFRecordBound layer). The Pinia
    // chatStore's bootstrap re-runs on the new view and pulls fresh
    // session data.
    try {
      const respBody = (await res.json()) as { csrf_token?: string }
      if (respBody?.csrf_token) {
        useCsrfStore().setToken(respBody.csrf_token)
      }
    } catch {
      // Best-effort — a malformed 200 body falls through to navigation.
      // The next authenticated request will 403 if the token is wrong;
      // the SPA's 401/403 handler kicks back to /login.
    }
    await router.push('/chat')
  } catch (err) {
    // Network-level failure (DNS, CORS, offline). Surface a generic
    // error — the underlying cause is logged via the browser's
    // network panel for debugging.
    showToast({
      message: 'Could not reach the server. Try again.',
      variant: 'error',
    })
    // eslint-disable-next-line no-console
    console.error('[flowstate] login fetch failed:', err)
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped>
.login-view {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100vh;
  padding: 1rem;
  background: var(--color-bg);
}

.login-card {
  width: 100%;
  max-width: 24rem;
  padding: 2rem;
  background: var(--color-surface);
  border-radius: 0.5rem;
  border: 1px solid var(--color-border);
}

.login-card h1 {
  margin: 0 0 0.5rem 0;
  font-size: 1.5rem;
}

.login-subtitle {
  margin: 0 0 1.5rem 0;
  color: var(--color-text-secondary);
  font-size: 0.9rem;
}

.field-label {
  display: block;
  margin-top: 1rem;
  margin-bottom: 0.25rem;
  font-weight: 500;
}

.field-input {
  display: block;
  width: 100%;
  padding: 0.5rem;
  background: var(--color-bg);
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  color: var(--color-text);
  font-size: 1rem;
  box-sizing: border-box;
}

.field-hint {
  margin: 0.5rem 0 0 0;
  font-size: 0.85rem;
  color: var(--color-text-secondary);
}

.login-secret-section {
  margin-top: 1rem;
  padding: 0.5rem 0;
}

.login-secret-section summary {
  cursor: pointer;
  font-size: 0.9rem;
  color: var(--color-text-secondary);
}

.login-submit {
  margin-top: 1.5rem;
  padding: 0.75rem 1rem;
  width: 100%;
  background: var(--color-accent);
  color: var(--color-bg);
  border: none;
  border-radius: 0.25rem;
  font-size: 1rem;
  font-weight: 500;
  cursor: pointer;
}

.login-submit:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
