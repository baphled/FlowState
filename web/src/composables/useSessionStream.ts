export const SSE_RECONNECT_BASE_DELAY_MS = 1000
export const SSE_RECONNECT_MAX_DELAY_MS = 30000
export const SSE_RECONNECT_MAX_ATTEMPTS = 5

import { subscribeSessionStream } from '@/api'

// SSE stall watchdog timeout. The engine's idle-stream watchdog
// (internal/engine/engine.go: engineStreamIdleTimeout = 60s, May 2026
// fix for the mid-thinking-halt incident) emits a synthetic Done
// within 60s of provider-stream silence; this client-side watchdog
// is the local backstop that surfaces the same condition to the user
// if the engine somehow fails to emit. 65s = 60s engine threshold +
// 5s scheduling buffer.
//
// Pre-May-2026 the per-phase map varied wildly (45s generating /
// 120s thinking / 180s tool_executing / 300s queued) because the
// frontend was the only place that could detect a hung stream. With
// the engine-side watchdog in place that variation is no longer
// load-bearing: every phase trips off the same 65s contract because
// the engine guarantees a chunk or a Done within 60s regardless of
// phase. The per-phase map is preserved as a seam in case future
// work needs to relax it for a specific phase, but every entry now
// matches the flat default.
export const SSE_STALL_TIMEOUT_MS = 65_000

export const SSE_STALL_TIMEOUT_BY_PHASE_MS: Record<string, number> = {
  generating: 65_000,
  thinking: 65_000,
  tool_executing: 65_000,
  queued: 65_000,
}

export function stallTimeoutForPhase(phase: string | undefined): number {
  if (phase && SSE_STALL_TIMEOUT_BY_PHASE_MS[phase] !== undefined) {
    return SSE_STALL_TIMEOUT_BY_PHASE_MS[phase]
  }
  return SSE_STALL_TIMEOUT_MS
}

export interface SessionStreamCallbacks {
  onMessage: (payload: string) => void
  onError: () => void
  onReconnect?: (attempt: number, delayMs: number) => void
  onStall: () => void
}

export interface SessionStream {
  connect(sessionId: string, callbacks: SessionStreamCallbacks): void
  disconnect(): void
  armWatchdog(onTrip: () => void, timeoutMs?: number): void
  clearWatchdog(): void
  isActive(): boolean
  reconnectAttempts(): number
}

export function useSessionStream(): SessionStream {
  let activeEventSource: EventSource | null = null
  let stallWatchdog: ReturnType<typeof setTimeout> | null = null
  let disconnected = false
  let reconnectAttemptCount = 0
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let isAutoReconnect = false

  function clearReconnectTimer(): void {
    if (reconnectTimer !== null) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
  }

  function disconnect(): void {
    clearReconnectTimer()
    if (activeEventSource !== null) {
      activeEventSource.close()
      activeEventSource = null
    }
    clearWatchdog()
    disconnected = true
  }

  function clearWatchdog(): void {
    if (stallWatchdog !== null) {
      clearTimeout(stallWatchdog)
      stallWatchdog = null
    }
  }

  function armWatchdog(onTrip: () => void, timeoutMs?: number): void {
    clearWatchdog()
    const effective = typeof timeoutMs === 'number' && timeoutMs > 0 ? timeoutMs : SSE_STALL_TIMEOUT_MS
    stallWatchdog = setTimeout(() => {
      stallWatchdog = null
      onTrip()
    }, effective)
  }

  function scheduleReconnect(sessionId: string, callbacks: SessionStreamCallbacks): void {
    if (reconnectAttemptCount >= SSE_RECONNECT_MAX_ATTEMPTS) {
      reconnectAttemptCount = 0
      isAutoReconnect = false
      callbacks.onError()
      return
    }
    reconnectAttemptCount += 1
    const delay = Math.min(
      SSE_RECONNECT_BASE_DELAY_MS * Math.pow(2, reconnectAttemptCount - 1),
      SSE_RECONNECT_MAX_DELAY_MS,
    )
    if (callbacks.onReconnect) {
      callbacks.onReconnect(reconnectAttemptCount, delay)
    }
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null
      isAutoReconnect = true
      connect(sessionId, callbacks)
    }, delay)
  }

  function connect(sessionId: string, callbacks: SessionStreamCallbacks): void {
    clearReconnectTimer()
    if (!isAutoReconnect) {
      reconnectAttemptCount = 0
    }
    isAutoReconnect = false
    disconnect()
    disconnected = false

    activeEventSource = subscribeSessionStream(sessionId)
    activeEventSource.addEventListener('message', (event) => {
      if (disconnected) return
      reconnectAttemptCount = 0
      const payload = (event as MessageEvent).data as string
      callbacks.onMessage(payload)
    })
    activeEventSource.addEventListener('error', () => {
      if (disconnected) return
      activeEventSource?.close()
      activeEventSource = null
      scheduleReconnect(sessionId, callbacks)
    })
    armWatchdog(callbacks.onStall)
  }

  function isActive(): boolean {
    return activeEventSource !== null
  }

  function getReconnectAttempts(): number {
    return reconnectAttemptCount
  }

  return {
    connect,
    disconnect,
    armWatchdog,
    clearWatchdog,
    isActive,
    reconnectAttempts: getReconnectAttempts,
  }
}
