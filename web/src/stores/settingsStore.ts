import { defineStore } from 'pinia'
import type { Theme } from '@/types'

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
  return readLocalStorage(API_HOST_STORAGE_KEY) ?? DEFAULT_API_HOST
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
