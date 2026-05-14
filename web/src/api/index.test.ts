import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { sendSessionMessage } from './index'

describe('sendSessionMessage', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify({}), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      )
    )
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('rejects immediately if signal is already aborted (does not call fetch)', async () => {
    const controller = new AbortController()
    controller.abort()
    let error: unknown
    try {
      await sendSessionMessage('sess-1', 'hello', { signal: controller.signal })
    } catch (e) {
      error = e
    }
    expect(fetchMock).not.toHaveBeenCalled()
    expect(error).toBeDefined()
    expect(
      error instanceof DOMException
        ? error.name
        : (error as Error).message
    ).toMatch(/abort/i)
  })
})
