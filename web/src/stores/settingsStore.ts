import { defineStore } from 'pinia'
import type { Theme } from '@/types'
import { isAllowedApiHost } from '@/lib/apiHostAllowlist'

// Single-user assumption (security LOW #7): all storage keys below are
// shared per-origin; FlowState currently assumes a single user per browser
// profile. Multi-user support requires a per-user prefix (e.g.
// `flowstate:${userId}:*`) gated behind an authenticated session — defer
// until auth lands. See SECURITY.md (TODO) for the migration plan.
const THEME_STORAGE_KEY = 'flowstate-theme'
const API_HOST_STORAGE_KEY = 'flowstate-api-host'
const SWARM_PANE_STORAGE_KEY = 'flowstate-swarm-pane-visible'
const CHAT_SIDEBAR_WIDTH_STORAGE_KEY = 'flowstate-chat-sidebar-width'

const DEFAULT_API_HOST = '/api'
const DEFAULT_CHAT_SIDEBAR_WIDTH = 360
const MIN_CHAT_SIDEBAR_WIDTH = 280
const MAX_CHAT_SIDEBAR_WIDTH = 520

function readLocalStorage(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function writeLocalStorage(key: string, value: string): void {
  try {
    localStorage.setItem(key, value)
  } catch {
    return
  }
}

function readTheme(): Theme {
  const value = readLocalStorage(THEME_STORAGE_KEY)
  if (value === 'light' || value === 'terminal') {
    return value
  }
  return 'dark'
}

function applyTheme(theme: Theme): void {
  if (typeof document === 'undefined') {
    return
  }

  document.documentElement.setAttribute('data-theme', theme)
}

function readApiHost(): string {
  const stored = readLocalStorage(API_HOST_STORAGE_KEY)
  if (stored === null) return DEFAULT_API_HOST
  // Defence-in-depth: api/index.ts also validates on every read, but the
  // settings UI surfaces the live value to the user — we don't want the
  // form to display a hostile entry as if it were valid. A rejected value
  // is treated as if no override were set.
  if (!isAllowedApiHost(stored)) {
    return DEFAULT_API_HOST
  }
  return stored
}

function readSwarmPaneVisible(): boolean {
  return readLocalStorage(SWARM_PANE_STORAGE_KEY) !== 'false'
}

function clampChatSidebarWidth(value: number): number {
  if (!Number.isFinite(value)) {
    return DEFAULT_CHAT_SIDEBAR_WIDTH
  }

  return Math.min(MAX_CHAT_SIDEBAR_WIDTH, Math.max(MIN_CHAT_SIDEBAR_WIDTH, value))
}

function readChatSidebarWidth(): number {
  return clampChatSidebarWidth(Number(readLocalStorage(CHAT_SIDEBAR_WIDTH_STORAGE_KEY) ?? DEFAULT_CHAT_SIDEBAR_WIDTH))
}

export const useSettingsStore = defineStore('settings', {
  state: () => ({
    theme: readTheme(),
    apiHost: readApiHost(),
    swarmPaneVisible: readSwarmPaneVisible(),
    chatSidebarWidth: readChatSidebarWidth(),
  }),

  actions: {
    setTheme(theme: Theme): void {
      this.theme = theme
      writeLocalStorage(THEME_STORAGE_KEY, theme)
      applyTheme(theme)
    },

    setApiHost(apiHost: string): void {
      // Reject hostile overrides at the write site. The live store value
      // and the persisted localStorage entry are reverted to the default
      // when validation fails. apiHostAllowlist logs a warn for
      // observability; we deliberately do not throw — a bad form input
      // should bounce visibly via the live store, not crash the app.
      if (!isAllowedApiHost(apiHost)) {
        // eslint-disable-next-line no-console
        console.warn(
          '[flowstate] settingsStore.setApiHost rejected value (allowlist policy):',
          apiHost,
        )
        this.apiHost = DEFAULT_API_HOST
        writeLocalStorage(API_HOST_STORAGE_KEY, DEFAULT_API_HOST)
        return
      }
      this.apiHost = apiHost
      writeLocalStorage(API_HOST_STORAGE_KEY, apiHost)
    },

    toggleSwarmPane(): void {
      this.swarmPaneVisible = !this.swarmPaneVisible
      writeLocalStorage(SWARM_PANE_STORAGE_KEY, String(this.swarmPaneVisible))
    },

    setSwarmPaneVisible(visible: boolean): void {
      this.swarmPaneVisible = visible
      writeLocalStorage(SWARM_PANE_STORAGE_KEY, String(visible))
    },

    setChatSidebarWidth(width: number): void {
      this.chatSidebarWidth = clampChatSidebarWidth(width)
      writeLocalStorage(CHAT_SIDEBAR_WIDTH_STORAGE_KEY, String(this.chatSidebarWidth))
    },
  },
})

applyTheme(readTheme())
