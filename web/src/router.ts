import { createRouter, createWebHistory } from 'vue-router'
import ChatView from '@/views/ChatView.vue'
import SwarmView from '@/views/SwarmView.vue'
import SettingsView from '@/views/SettingsView.vue'
import AgentInfoView from '@/views/AgentInfoView.vue'
import LoginView from '@/views/LoginView.vue'
import ProviderQuotaDashboardView from '@/views/ProviderQuotaDashboardView.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/chat' },
    { path: '/chat', component: ChatView, name: 'chat' },
    { path: '/swarm', component: SwarmView, name: 'swarm' },
    { path: '/settings', component: SettingsView, name: 'settings' },
    { path: '/agents/:id', component: AgentInfoView, name: 'agent-info' },
    // PR3/C8 — login surface for the FlowState API Auth Track. Flag-
    // gated server-side at PR3/C7 (features.auth_v1); navigation to
    // /login is always allowed (no guard) because un-authenticated
    // users must be able to reach the login screen. The auth redirect
    // flow (401-on-fetch → push('/login')) lives at the API-layer
    // catch sites; the router itself stays a passive resource.
    //
    // Plan §"Migration Path" line 869: "On the next API call, the
    // server returns 401, the SPA redirects to /login, the user
    // enters credentials, the SPA reconnects."
    { path: '/login', component: LoginView, name: 'login' },
    // Provider Quota and Spend Visibility plan (May 2026) — PR5a.
    // No router-level auth guard: the dashboard's GET fetch hits
    // /api/v1/providers/quota which is registerProtected on the
    // backend; a 401 propagates through fetchProviderQuotas() as a
    // thrown Error which the view catches and surfaces as an empty
    // state. This matches the SPA-wide pattern PR3 established —
    // the router stays a passive resource and the API layer drives
    // the auth-fail flow.
    {
      path: '/providers/quota',
      component: ProviderQuotaDashboardView,
      name: 'provider-quota-dashboard',
    },
  ],
})

export default router
