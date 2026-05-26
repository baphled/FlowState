import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { detectCarbonylMode, useUIStore } from './uiStore'

/**
 * uiStore behaviour specs — pin the carbonyl-mode boot contract.
 *
 * Contract:
 *   - detectCarbonylMode parses URLSearchParams from the given string
 *     and returns true ONLY when `carbonyl=1` is present.
 *   - useUIStore.carbonylMode defaults to false (browser mode).
 *   - setCarbonylMode(true) flips the store flag; consumers see it.
 *
 * The boot path in main.ts is exercised indirectly: it calls
 * detectCarbonylMode() once, then conditionally adds the body class
 * and seeds the store. Both branches are tested here.
 */

describe('detectCarbonylMode', () => {
  it('returns true when carbonyl=1 is present', () => {
    expect(detectCarbonylMode('?carbonyl=1')).toBe(true)
  })

  it('returns true when carbonyl=1 is among other params', () => {
    expect(detectCarbonylMode('?session=abc&carbonyl=1&agent=x')).toBe(true)
  })

  it('returns false on an empty query string', () => {
    expect(detectCarbonylMode('')).toBe(false)
  })

  it('returns false when carbonyl flag is absent', () => {
    expect(detectCarbonylMode('?session=abc&agent=x')).toBe(false)
  })

  it('returns false when carbonyl has a non-1 value', () => {
    // Strict equality on "1" — we never want a stray `carbonyl=true` or
    // `carbonyl=0` to flip the focus-stealing behaviour by accident.
    expect(detectCarbonylMode('?carbonyl=0')).toBe(false)
    expect(detectCarbonylMode('?carbonyl=true')).toBe(false)
    expect(detectCarbonylMode('?carbonyl=')).toBe(false)
  })
})

describe('useUIStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('defaults carbonylMode to false', () => {
    const ui = useUIStore()
    expect(ui.carbonylMode).toBe(false)
  })

  it('setCarbonylMode flips the flag', () => {
    const ui = useUIStore()
    ui.setCarbonylMode(true)
    expect(ui.carbonylMode).toBe(true)
    ui.setCarbonylMode(false)
    expect(ui.carbonylMode).toBe(false)
  })
})

describe('carbonyl-mode boot integration', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    document.body.classList.remove('carbonyl-mode')
  })

  it('adds body.carbonyl-mode and seeds the store when ?carbonyl=1 is detected', () => {
    // Simulate the main.ts boot block. We don't import main.ts directly
    // (it has top-level side effects that mount the app) — instead we
    // replicate the conditional so the contract stays in one place.
    if (detectCarbonylMode('?carbonyl=1')) {
      document.body.classList.add('carbonyl-mode')
      useUIStore().setCarbonylMode(true)
    }

    expect(document.body.classList.contains('carbonyl-mode')).toBe(true)
    expect(useUIStore().carbonylMode).toBe(true)
  })

  it('leaves body and store untouched when carbonyl flag is absent (browser mode)', () => {
    if (detectCarbonylMode('?session=abc')) {
      document.body.classList.add('carbonyl-mode')
      useUIStore().setCarbonylMode(true)
    }

    expect(document.body.classList.contains('carbonyl-mode')).toBe(false)
    expect(useUIStore().carbonylMode).toBe(false)
  })
})
