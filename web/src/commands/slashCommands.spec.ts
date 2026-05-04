import { describe, expect, it } from 'vitest'
import { SLASH_COMMANDS } from './slashCommands'

describe('SLASH_COMMANDS', () => {
  it('mirrors the TUI builtins in registration order', () => {
    // Source of truth: internal/tui/intents/chat/slashcommand/builtins.go
    // RegisterBuiltins. Keep this order in sync.
    expect(SLASH_COMMANDS.map((c) => c.name)).toEqual([
      'clear',
      'help',
      'exit',
      'quit',
      'sessions',
      'plans',
      'agent',
      'agents',
      'model',
      'swarm',
      'autoresearch',
    ])
  })

  it('every command has a non-empty description', () => {
    for (const cmd of SLASH_COMMANDS) {
      expect(cmd.description.length).toBeGreaterThan(0)
    }
  })
})
