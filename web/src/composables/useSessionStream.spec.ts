import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SSE_STALL_TIMEOUT_MS, useSessionStream } from './useSessionStream'

// FakeEventSource mirrors the pattern used in chatStore.test.ts so the
// composable's tests exercise the same global-EventSource swap mechanism the
// store-level tests already rely on. Tests that assert on the most recently
// constructed source read the static `instances` array; reset it in
// beforeEach so test order does not matter.
class FakeEventSource {
  static instances: FakeEventSource[] = []
  url: string
  listeners: Record<string, (event: MessageEvent) => void> = {}
  closed = false

  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }

  addEventListener(type: string, fn: (event: MessageEvent) => void): void {
    this.listeners[type] = fn
  }

  removeEventListener(type: string): void {
    delete this.listeners[type]
  }

  close(): void {
    this.closed = true
  }

  fire(type: string, data: unknown): void {
    const fn = this.listeners[type]
    if (fn) {
      fn({ data: typeof data === 'string' ? data : JSON.stringify(data) } as MessageEvent)
    }
  }
}

// The composable imports subscribeSessionStream from @/api which calls
// `new EventSource(...)`. Mocking the api module to return a FakeEventSource
// keeps the test contract focused on the composable's lifecycle, not on URL
// construction.
vi.mock('@/api', () => ({
  subscribeSessionStream: vi.fn(
    (sessionId: string) =>
      new FakeEventSource(`/api/v1/sessions/${sessionId}/stream`) as unknown as EventSource,
  ),
}))

describe('useSessionStream', () => {
  beforeEach(() => {
    FakeEventSource.instances.length = 0
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  describe('connect', () => {
    it('opens an EventSource and registers message + error listeners', () => {
      const stream = useSessionStream()
      const onMessage = vi.fn()
      const onError = vi.fn()
      const onStall = vi.fn()

      stream.connect('session-1', { onMessage, onError, onStall })

      expect(FakeEventSource.instances).toHaveLength(1)
      const es = FakeEventSource.instances[0]
      expect(es.listeners.message).toBeDefined()
      expect(es.listeners.error).toBeDefined()

      // Verify the listeners route to the supplied callbacks. The contract is
      // raw payload pass-through — JSON parsing belongs to the consumer.
      es.fire('message', 'chunk-payload')
      expect(onMessage).toHaveBeenCalledWith('chunk-payload')

      es.fire('error', null)
      expect(onError).toHaveBeenCalledOnce()

      stream.disconnect()
    })

    it('tears down the prior connection before opening a new one', () => {
      const stream = useSessionStream()
      const callbacks = { onMessage: vi.fn(), onError: vi.fn(), onStall: vi.fn() }

      stream.connect('session-1', callbacks)
      const first = FakeEventSource.instances[0]
      expect(first.closed).toBe(false)

      stream.connect('session-2', callbacks)
      const second = FakeEventSource.instances[1]

      // The first connection must have been closed by the second connect call —
      // without this the broker would register a duplicate subscriber and the
      // next chunk would arrive twice.
      expect(first.closed).toBe(true)
      expect(second.closed).toBe(false)
      expect(stream.isActive()).toBe(true)

      stream.disconnect()
    })
  })

  describe('disconnect', () => {
    it('closes the active EventSource and clears the watchdog', () => {
      vi.useFakeTimers()
      const stream = useSessionStream()
      const onStall = vi.fn()

      stream.connect('session-1', { onMessage: vi.fn(), onError: vi.fn(), onStall })
      const es = FakeEventSource.instances[0]
      expect(stream.isActive()).toBe(true)

      stream.disconnect()

      expect(es.closed).toBe(true)
      expect(stream.isActive()).toBe(false)

      // The watchdog armed by connect must NOT trip after disconnect — the
      // consumer is no longer interested in stall signals from a closed
      // stream.
      vi.advanceTimersByTime(SSE_STALL_TIMEOUT_MS + 1000)
      expect(onStall).not.toHaveBeenCalled()
    })

    it('is a no-op when nothing is active', () => {
      const stream = useSessionStream()
      // Call disconnect without any connect. Must not throw and isActive must
      // remain false.
      expect(() => stream.disconnect()).not.toThrow()
      expect(stream.isActive()).toBe(false)
    })
  })

  describe('armWatchdog', () => {
    it('fires onTrip after SSE_STALL_TIMEOUT_MS when nothing clears it', () => {
      vi.useFakeTimers()
      const stream = useSessionStream()
      const onTrip = vi.fn()

      stream.armWatchdog(onTrip)
      expect(onTrip).not.toHaveBeenCalled()

      vi.advanceTimersByTime(SSE_STALL_TIMEOUT_MS - 1)
      expect(onTrip).not.toHaveBeenCalled()

      vi.advanceTimersByTime(1)
      expect(onTrip).toHaveBeenCalledOnce()
    })

    it('replaces the prior timer when called a second time (idempotent re-arm)', () => {
      vi.useFakeTimers()
      const stream = useSessionStream()
      const firstOnTrip = vi.fn()
      const secondOnTrip = vi.fn()

      stream.armWatchdog(firstOnTrip)
      // Re-arm shortly after — the original timer must be cancelled so it
      // never fires firstOnTrip; only the most recently armed callback should
      // fire after the next full timeout window.
      vi.advanceTimersByTime(1000)
      stream.armWatchdog(secondOnTrip)

      // Total elapsed since first arm: 1000 + (TIMEOUT - 1). If the first
      // timer were still alive it would have tripped already.
      vi.advanceTimersByTime(SSE_STALL_TIMEOUT_MS - 1)
      expect(firstOnTrip).not.toHaveBeenCalled()
      expect(secondOnTrip).not.toHaveBeenCalled()

      vi.advanceTimersByTime(1)
      expect(firstOnTrip).not.toHaveBeenCalled()
      expect(secondOnTrip).toHaveBeenCalledOnce()
    })
  })

  describe('clearWatchdog', () => {
    it('cancels a pending watchdog without firing it', () => {
      vi.useFakeTimers()
      const stream = useSessionStream()
      const onTrip = vi.fn()

      stream.armWatchdog(onTrip)
      stream.clearWatchdog()

      vi.advanceTimersByTime(SSE_STALL_TIMEOUT_MS + 1000)
      expect(onTrip).not.toHaveBeenCalled()
    })

    it('is a no-op when no watchdog is armed', () => {
      const stream = useSessionStream()
      expect(() => stream.clearWatchdog()).not.toThrow()
    })
  })

  describe('isActive', () => {
    it('reflects connect / disconnect transitions', () => {
      const stream = useSessionStream()
      const callbacks = { onMessage: vi.fn(), onError: vi.fn(), onStall: vi.fn() }

      expect(stream.isActive()).toBe(false)

      stream.connect('session-1', callbacks)
      expect(stream.isActive()).toBe(true)

      stream.disconnect()
      expect(stream.isActive()).toBe(false)
    })
  })
})
