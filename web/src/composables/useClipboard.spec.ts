import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { useClipboard } from './useClipboard'

describe('useClipboard', () => {
  const writeText = vi.fn()

  beforeEach(() => {
    vi.useFakeTimers()
    writeText.mockReset()
    vi.stubGlobal('navigator', {
      clipboard: {
        writeText,
      },
    } as unknown as Navigator)
  })

  afterEach(() => {
    vi.runOnlyPendingTimers()
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('copies text to the clipboard and marks copied true', async () => {
    writeText.mockResolvedValueOnce(undefined)
    const { copy, copied, error } = useClipboard()

    await copy('hello')

    expect(writeText).toHaveBeenCalledWith('hello')
    expect(copied.value).toBe(true)
    expect(error.value).toBeNull()
  })

  it('reverts copied to false after 2000ms', async () => {
    writeText.mockResolvedValueOnce(undefined)
    const { copy, copied } = useClipboard()

    await copy('hello')
    expect(copied.value).toBe(true)

    vi.advanceTimersByTime(2000)
    await nextTick()

    expect(copied.value).toBe(false)
  })

  it('sets an error when clipboard writing fails', async () => {
    writeText.mockRejectedValueOnce(new Error('clipboard unavailable'))
    const { copy, copied, error } = useClipboard()

    await copy('hello')

    expect(copied.value).toBe(false)
    expect(error.value).toBe('clipboard unavailable')
  })

  it('resets the timeout on rapid successive copies', async () => {
    writeText.mockResolvedValue(undefined)
    const { copy, copied } = useClipboard()

    await copy('first')
    vi.advanceTimersByTime(1500)
    await copy('second')

    vi.advanceTimersByTime(600)
    await nextTick()

    expect(copied.value).toBe(true)

    vi.advanceTimersByTime(1400)
    await nextTick()

    expect(copied.value).toBe(false)
  })
})
