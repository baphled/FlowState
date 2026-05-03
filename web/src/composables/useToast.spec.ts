import { beforeEach, describe, expect, it, vi, afterEach } from 'vitest'
import { dismissToast, showToast, useToast } from './useToast'

describe('useToast', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
    const { toasts, dismissAll } = useToast()
    dismissAll()
    while (toasts.value.length > 0) {
      toasts.value.pop()
    }
  })

  it('returns an empty toasts array initially', () => {
    const { toasts } = useToast()

    expect(toasts.value).toEqual([])
  })

  it('adds a toast when showToast is called with a string', () => {
    const { toasts } = useToast()

    showToast('Hello world')

    expect(toasts.value).toHaveLength(1)
    expect(toasts.value[0].message).toBe('Hello world')
    expect(toasts.value[0].variant).toBe('default')
  })

  it('adds a toast when showToast is called with options', () => {
    const { toasts } = useToast()

    showToast({
      message: 'Saved successfully',
      title: 'Success',
      variant: 'success',
    })

    expect(toasts.value).toHaveLength(1)
    expect(toasts.value[0].message).toBe('Saved successfully')
    expect(toasts.value[0].title).toBe('Success')
    expect(toasts.value[0].variant).toBe('success')
  })

  it('assigns a unique id to each toast', () => {
    const { toasts } = useToast()

    showToast('First')
    showToast('Second')

    expect(toasts.value[0].id).not.toBe(toasts.value[1].id)
  })

  it('uses default duration of 3000ms', () => {
    const { toasts } = useToast()

    showToast('Timed message')

    expect(toasts.value[0].duration).toBe(3000)
  })

  it('respects custom duration when provided', () => {
    const { toasts } = useToast()

    showToast({ message: 'Quick', duration: 500 })

    expect(toasts.value[0].duration).toBe(500)
  })

  it('auto-dismisses after duration', () => {
    const { toasts } = useToast()

    showToast({ message: 'Gone soon', duration: 1000 })

    expect(toasts.value).toHaveLength(1)

    vi.advanceTimersByTime(1000)

    expect(toasts.value).toHaveLength(0)
  })

  it('does not auto-dismiss when duration is 0', () => {
    const { toasts } = useToast()

    showToast({ message: 'Sticky', duration: 0 })

    expect(toasts.value).toHaveLength(1)

    vi.advanceTimersByTime(10000)

    expect(toasts.value).toHaveLength(1)
  })

  it('dismisses toast via removeToast', () => {
    const { toasts, removeToast } = useToast()

    showToast('First')
    showToast('Second')
    const id = toasts.value[0].id

    removeToast(id)

    expect(toasts.value).toHaveLength(1)
    expect(toasts.value[0].message).toBe('Second')
  })

  it('dismisses toast via dismissToast with id', () => {
    const { toasts } = useToast()

    showToast('First')
    showToast('Second')
    const id = toasts.value[1].id

    dismissToast(id)

    expect(toasts.value).toHaveLength(1)
    expect(toasts.value[0].message).toBe('First')
  })

  it('dismissAll removes all toasts', () => {
    const { toasts, dismissAll } = useToast()

    showToast('First')
    showToast('Second')
    showToast('Third')

    dismissAll()

    expect(toasts.value).toHaveLength(0)
  })

  it('stores action label and callback when provided', () => {
    const { toasts } = useToast()
    const onClick = vi.fn()

    showToast({
      message: 'Undo available',
      action: { label: 'Undo', onClick },
    })

    expect(toasts.value[0].action).toBeDefined()
    expect(toasts.value[0].action?.label).toBe('Undo')
    expect(toasts.value[0].action?.onClick).toBe(onClick)
  })

  it('triggers action callback when called', () => {
    const { toasts } = useToast()
    const onClick = vi.fn()

    showToast({
      message: 'With action',
      action: { label: 'Retry', onClick },
    })

    toasts.value[0].action?.onClick()

    expect(onClick).toHaveBeenCalledOnce()
  })

  it('multiple showToast calls stack toasts', () => {
    const { toasts } = useToast()

    showToast('One')
    showToast('Two')
    showToast('Three')

    expect(toasts.value).toHaveLength(3)
    expect(toasts.value[0].message).toBe('One')
    expect(toasts.value[1].message).toBe('Two')
    expect(toasts.value[2].message).toBe('Three')
  })

  it('loading variant defaults to persistent (duration 0)', () => {
    const { toasts } = useToast()

    showToast({ message: 'Loading...', variant: 'loading' })

    expect(toasts.value[0].duration).toBe(0)
    expect(toasts.value[0].variant).toBe('loading')
  })

  it('explicit duration overrides loading variant default', () => {
    const { toasts } = useToast()

    showToast({ message: 'Loading...', variant: 'loading', duration: 5000 })

    expect(toasts.value[0].duration).toBe(5000)
  })

  it('error variant uses default duration', () => {
    const { toasts } = useToast()

    showToast({ message: 'Oops', variant: 'error' })

    expect(toasts.value[0].variant).toBe('error')
    expect(toasts.value[0].duration).toBe(3000)
  })

  it('clears auto-dismiss timer when toast is manually dismissed', () => {
    const { toasts, removeToast } = useToast()
    const consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {})

    showToast({ message: 'Gone soon', duration: 1000 })
    const id = toasts.value[0].id

    removeToast(id)
    vi.advanceTimersByTime(2000)

    expect(toasts.value).toHaveLength(0)
    consoleSpy.mockRestore()
  })
})
