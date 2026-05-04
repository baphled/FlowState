import { describe, expect, it } from 'vitest'
import { detectTrigger, insertToken } from './useInputTriggers'

describe('detectTrigger', () => {
  describe('slash trigger', () => {
    it('opens at the start of the buffer when "/" is the first char', () => {
      const trigger = detectTrigger('/', 1)
      expect(trigger).toEqual({
        kind: 'slash',
        triggerIndex: 0,
        caretIndex: 1,
        fragment: '',
      })
    })

    it('captures the fragment between the slash and the caret', () => {
      const trigger = detectTrigger('/cle', 4)
      expect(trigger?.kind).toBe('slash')
      expect(trigger?.fragment).toBe('cle')
    })

    it('opens after a newline (fresh trigger char on a new line)', () => {
      const trigger = detectTrigger('hello\n/he', 9)
      expect(trigger?.kind).toBe('slash')
      expect(trigger?.fragment).toBe('he')
    })

    it('does NOT open mid-word so paths like "src/foo" are safe', () => {
      const trigger = detectTrigger('src/foo', 7)
      expect(trigger).toBeNull()
    })

    it('closes once the user types a space after the command', () => {
      const trigger = detectTrigger('/clear ', 7)
      expect(trigger).toBeNull()
    })
  })

  describe('mention trigger', () => {
    it('opens at the start of the buffer', () => {
      const trigger = detectTrigger('@', 1)
      expect(trigger).toEqual({
        kind: 'mention',
        triggerIndex: 0,
        caretIndex: 1,
        fragment: '',
      })
    })

    it('opens after a space (mid-message)', () => {
      const trigger = detectTrigger('hey @plan', 9)
      expect(trigger?.kind).toBe('mention')
      expect(trigger?.fragment).toBe('plan')
      expect(trigger?.triggerIndex).toBe(4)
    })

    it('opens after a newline', () => {
      const trigger = detectTrigger('first line\n@coder', 17)
      expect(trigger?.kind).toBe('mention')
      expect(trigger?.fragment).toBe('coder')
    })

    it('does NOT open mid-word so emails like "foo@bar" are safe', () => {
      const trigger = detectTrigger('foo@bar', 7)
      expect(trigger).toBeNull()
    })

    it('closes once the user types a space after the mention', () => {
      const trigger = detectTrigger('@planner ', 9)
      expect(trigger).toBeNull()
    })
  })

  it('returns null when caret is at the start with no trigger', () => {
    expect(detectTrigger('', 0)).toBeNull()
    expect(detectTrigger('hello', 5)).toBeNull()
  })

  it('returns null for out-of-range caret', () => {
    expect(detectTrigger('hi', -1)).toBeNull()
    expect(detectTrigger('hi', 999)).toBeNull()
  })
})

describe('insertToken', () => {
  it('replaces a slash fragment with the token plus trailing space', () => {
    const trigger = { kind: 'slash' as const, triggerIndex: 0, caretIndex: 4, fragment: 'cle' }
    const result = insertToken('/cle', trigger, '/clear')
    expect(result.text).toBe('/clear ')
    expect(result.caret).toBe(7)
  })

  it('preserves text after the caret when inserting', () => {
    const trigger = { kind: 'mention' as const, triggerIndex: 4, caretIndex: 9, fragment: 'plan' }
    const result = insertToken('hey @plan rest', trigger, '@planner')
    expect(result.text).toBe('hey @planner  rest')
    expect(result.caret).toBe('hey @planner '.length)
  })

  it('replaces an empty fragment (just the trigger char) cleanly', () => {
    const trigger = { kind: 'slash' as const, triggerIndex: 0, caretIndex: 1, fragment: '' }
    const result = insertToken('/', trigger, '/help')
    expect(result.text).toBe('/help ')
    expect(result.caret).toBe(6)
  })

  it('inserts a mention token mid-message correctly', () => {
    const trigger = { kind: 'mention' as const, triggerIndex: 4, caretIndex: 5, fragment: '' }
    const result = insertToken('hey @ how are you', trigger, '@planner')
    expect(result.text).toBe('hey @planner  how are you')
  })
})
