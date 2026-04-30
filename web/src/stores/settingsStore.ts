import { ref, watch } from 'vue'
import { defineStore } from 'pinia'
import type { Theme } from '@/types'

const STORAGE_KEY = 'flowstate-settings'

interface PersistedSettings {
  theme: Theme
  apiHost: string
  swarmPaneVisible: boolean
}

function loadFromStorage(): PersistedSettings {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw) return JSON.parse(raw) as PersistedSettings
  } catch {
    // ignore parse errors
  }
  return { theme: 'dark', apiHost: 'http://localhost:8080', swarmPaneVisible: true }
}

export const useSettingsStore = defineStore('settings', () => {
  const saved = loadFromStorage()
  const theme = ref<Theme>(saved.theme)
  const apiHost = ref(saved.apiHost)
  const swarmPaneVisible = ref(saved.swarmPaneVisible)

  function applyTheme(t: Theme): void {
    document.documentElement.setAttribute('data-theme', t)
  }

  applyTheme(theme.value)

  watch(theme, (t) => applyTheme(t))

  watch([theme, apiHost, swarmPaneVisible], () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({
      theme: theme.value,
      apiHost: apiHost.value,
      swarmPaneVisible: swarmPaneVisible.value,
    }))
  })

  function toggleSwarmPane(): void {
    swarmPaneVisible.value = !swarmPaneVisible.value
  }

  function setTheme(t: Theme): void {
    theme.value = t
  }

  function setApiHost(host: string): void {
    apiHost.value = host
  }

  return { theme, apiHost, swarmPaneVisible, toggleSwarmPane, setTheme, setApiHost }
})
