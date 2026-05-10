import { ref, computed } from 'vue'
import { defineStore } from 'pinia'
import type { SwarmEvent } from '@/types'
import { joinBaseURL } from '@/api'
import { useChatStore } from '@/stores/chatStore'

const MAX_EVENTS = 500

export const useSwarmStore = defineStore('swarm', () => {
  const events = ref<SwarmEvent[]>([])
  const isLive = ref(false)
  const error = ref<string | null>(null)
  const abortController = ref<AbortController | null>(null)

  // Generation token. Every connect() increments this. The active read loop
  // captures the value at start and only mutates store state (events, isLive,
  // error) when its captured generation still matches the current. This pins
  // the M8 contract: a late `read()` resolution from a previous generation —
  // including the generation's `finally` block — must not touch the store.
  const generation = ref(0)

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
      if (typeof event.id !== 'string') return
      const idx = events.value.findIndex((e) => e.id === event.id)
      if (idx >= 0) {
        events.value[idx] = event
      } else {
        const next = [...events.value, event]
        // Evict oldest entries to keep memory bounded.
        events.value = next.length > MAX_EVENTS ? next.slice(next.length - MAX_EVENTS) : next
      }
    } catch {
      return
    }
  }

  async function connect(): Promise<void> {
    await disconnect()
    error.value = null

    // H5 follow-up: GET /api/swarm/events now requires ?session_id=<id>. Read
    // the active session from the canonical source (chatStore.currentSessionId
    // — the same field NavBar/SessionSwitcher/QueuedPromptStrip read). If the
    // caller invoked connect() with no active session, fail loudly: no fetch,
    // a visible error string, and isLive stays false. A silent skip would mask
    // the upstream routing bug (button enabled with no session in scope).
    const sessionId = useChatStore().currentSessionId
    if (!sessionId) {
      error.value = 'cannot connect to swarm events: no active session id'
      isLive.value = false
      return
    }

    // Claim a fresh generation for this loop. The captured value below is the
    // *only* token this invocation will check before mutating shared state.
    generation.value += 1
    const myGeneration = generation.value

    isLive.value = true
    abortController.value = new AbortController()

    const url = joinBaseURL(`/swarm/events?session_id=${encodeURIComponent(sessionId)}`)

    try {
      const response = await fetch(url, {
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
        // Generation check on EVERY post-await resumption: if a newer connect()
        // has fired, this loop is stale and must not append events or flip
        // isLive/error. Just exit silently.
        if (myGeneration !== generation.value) {
          return
        }
        if (done) {
          // Flush any remaining bytes held by the decoder's internal state,
          // then process whatever is left in the line buffer.
          buffer += decoder.decode()
          if (buffer) {
            ingestEventLine(buffer)
          }
          break
        }
        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''
        for (const line of lines) {
          ingestEventLine(line)
        }
      }
    } catch (e) {
      // Stale generation: swallow without touching error.value (newer
      // generation owns the slot now).
      if (myGeneration !== generation.value) {
        return
      }
      if (e instanceof Error && e.name !== 'AbortError') {
        error.value = e.message
      }
    } finally {
      // Same guard for the success path's finally — only the live generation
      // gets to flip isLive off.
      if (myGeneration === generation.value) {
        isLive.value = false
      }
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
