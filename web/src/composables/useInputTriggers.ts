/**
 * useInputTriggers — derives picker state from the raw textarea value
 * and caret position. Pure logic, no DOM access; the host component is
 * responsible for wiring it to the textarea's `value` and `selectionStart`.
 *
 * Triggers:
 *   - "/" at the start of the buffer, or as the first character on a
 *     fresh line, opens the slash-command picker. The "fragment" is
 *     everything between the slash and the caret (used as the fuzzy
 *     filter query). Whitespace closes the picker so users can keep
 *     typing arguments after the command name.
 *   - "@" preceded by start-of-buffer or whitespace opens the
 *     agent/swarm mention picker. Same fragment rule applies.
 *
 * The composable returns a pair of helpers:
 *   - detectTrigger(text, caret): pure function returning the active
 *     trigger descriptor (or null) for a given input/caret state.
 *   - insertToken(text, caret, trigger, token): pure function returning
 *     the new text + caret after replacing the trigger fragment with
 *     the token (e.g. "/clear " or "@planner ").
 */

export type TriggerKind = 'slash' | 'mention'

export interface TriggerDescriptor {
  /** Which picker to open. */
  readonly kind: TriggerKind
  /** Buffer index of the trigger character itself (the "/" or "@"). */
  readonly triggerIndex: number
  /** Caret index at the moment of detection. */
  readonly caretIndex: number
  /** Text between the trigger character and the caret — used as the fuzzy filter query. */
  readonly fragment: string
}

export interface TokenInsertion {
  /** New textarea value after replacing the trigger fragment with the token. */
  readonly text: string
  /** Caret position after the inserted token (always immediately after a trailing space). */
  readonly caret: number
}

/**
 * Returns true when the character at index-1 is start-of-buffer,
 * whitespace, or a newline. Used to gate "@" mention detection so the
 * trigger doesn't fire mid-word (e.g. inside an email address).
 */
function isAtBoundary(text: string, index: number): boolean {
  if (index <= 0) return true
  const prev = text[index - 1]
  return prev === ' ' || prev === '\n' || prev === '\t'
}

/**
 * Locates the most recent trigger character before the caret and
 * returns a descriptor when the fragment between trigger and caret is
 * still a valid fuzzy-filter query (no whitespace, no newline). Returns
 * null when no trigger is active — the host should close any open picker.
 */
export function detectTrigger(text: string, caret: number): TriggerDescriptor | null {
  if (caret < 0 || caret > text.length) return null

  // Walk backwards from the caret, looking for "/" or "@". Stop at any
  // whitespace — a fragment with whitespace means the user has moved on
  // from the trigger and the picker should close.
  for (let i = caret - 1; i >= 0; i -= 1) {
    const ch = text[i]
    if (ch === ' ' || ch === '\n' || ch === '\t') {
      return null
    }
    if (ch === '/') {
      // Slash only triggers at the start of the buffer or right after
      // a newline — it must not fire inside paths like "src/foo".
      if (i === 0 || text[i - 1] === '\n') {
        return {
          kind: 'slash',
          triggerIndex: i,
          caretIndex: caret,
          fragment: text.slice(i + 1, caret),
        }
      }
      return null
    }
    if (ch === '@') {
      if (isAtBoundary(text, i)) {
        return {
          kind: 'mention',
          triggerIndex: i,
          caretIndex: caret,
          fragment: text.slice(i + 1, caret),
        }
      }
      return null
    }
  }
  return null
}

/**
 * Replaces the trigger fragment (from triggerIndex to caretIndex) with
 * the given token plus a single trailing space, so the user can keep
 * typing arguments. The caret lands immediately after the trailing
 * space.
 */
export function insertToken(
  text: string,
  trigger: TriggerDescriptor,
  token: string,
): TokenInsertion {
  const before = text.slice(0, trigger.triggerIndex)
  const after = text.slice(trigger.caretIndex)
  const insertion = `${token} `
  return {
    text: `${before}${insertion}${after}`,
    caret: before.length + insertion.length,
  }
}
