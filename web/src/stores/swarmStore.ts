import { ref, computed } from 'vue'
import { defineStore } from 'pinia'
import type { SwarmEvent } from '@/types'

const API_BASE = '/api/swarm/events'

export const useSwarmStore = defineStore('swarm', () => {
  const events = ref<SwarmEvent[]>([])
  const isLive = ref(false)
  const error = ref<string | null>(null)
  const abortController = ref<AbortController | null>(null)

  function ingestEventLine(line: string): void {
    if (!line.startsWith('data: ')) {
      return
    }

    const data = line.slice(6)
    if (data === '[DONE]') {
      return
    }

    try {
      const event = JSON.parse(data) as SwarmEvent
      const idx = events.value.findIndex((e) => e.id === event.id)
      if (idx >= 0) {
        events.value[idx] = event
      } else {
        events.value = [...events.value, event]
      }
    } catch {
      return
    }
  }

  async function connect(): Promise<void> {
    await disconnect()
    error.value = null
    isLive.value = true
    abortController.value = new AbortController()

    try {
      const response = await fetch(API_BASE, {
        signal: abortController.value.signal,
      })

      if (!response.ok) {
        throw new Error(`HTTP ${response.status}`)
      }

      const reader = response.body?.getReader()
      if (!reader) {
        throw new Error('Response body is not readable')
      }

      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        for (const line of lines) {
          ingestEventLine(line)
        }

        if (done) {
          if (buffer) {
            ingestEventLine(buffer)
          }
          break
        }
      }
    } catch (e) {
      if (e instanceof Error && e.name !== 'AbortError') {
        error.value = e.message
      }
    } finally {
      isLive.value = false
    }
  }

  async function disconnect(): Promise<void> {
    if (abortController.value) {
      abortController.value.abort()
      abortController.value = null
    }
    isLive.value = false
  }

  // Expose computed for template
  const eventCount = computed(() => events.value.length)

  // Filter events by type
  const delegationEvents = computed(() =>
    events.value.filter(e => e.type === 'delegation')
  )

  const harnessEvents = computed(() =>
    events.value.filter(e =>
      e.type === 'harness_retry' ||
      e.type === 'harness_attempt_start' ||
      e.type === 'harness_complete' ||
      e.type === 'harness_critic_feedback'
    )
  )

  const toolEvents = computed(() =>
    events.value.filter(e =>
      e.type === 'tool_call' ||
      e.type === 'tool_result'
    )
  )

  // Filter events by plan artifacts
  const planEvents = computed(() =>
    events.value.filter(e =>
      e.type === 'plan'
    )
  )

  // Filter events by status transitions (any event with status field indicating state change)
  const statusEvents = computed(() =>
    events.value.filter(e =>
      e.status && (
        e.status === 'start' ||
        e.status === 'progress' ||
        e.status === 'complete' ||
        e.status === 'error'
      )
    )
  )

  // Filter events by review artifacts
  const reviewEvents = computed(() =>
    events.value.filter(e =>
      e.type === 'review'
    )
  )

  return {
    events,
    isLive,
    error,
    connect,
    disconnect,
    eventCount,
    delegationEvents,
    harnessEvents,
    toolEvents,
    planEvents,
    statusEvents,
    reviewEvents,
  }
})
