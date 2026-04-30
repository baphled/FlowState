import { ref } from 'vue'
import { defineStore } from 'pinia'
import { fetchSwarmEvents } from '@/api'
import type { SwarmEvent } from '@/types'

const POLL_INTERVAL_MS = 2000

export const useSwarmStore = defineStore('swarm', () => {
  const events = ref<SwarmEvent[]>([])
  const isPolling = ref(false)
  const error = ref<string | null>(null)
  let pollTimer: ReturnType<typeof setInterval> | null = null

  async function loadEvents(): Promise<void> {
    try {
      events.value = await fetchSwarmEvents()
      error.value = null
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load swarm events'
    }
  }

  function startPolling(): void {
    if (isPolling.value) return
    isPolling.value = true
    void loadEvents()
    pollTimer = setInterval(() => void loadEvents(), POLL_INTERVAL_MS)
  }

  function stopPolling(): void {
    if (pollTimer !== null) {
      clearInterval(pollTimer)
      pollTimer = null
    }
    isPolling.value = false
  }

  return { events, isPolling, error, loadEvents, startPolling, stopPolling }
})
