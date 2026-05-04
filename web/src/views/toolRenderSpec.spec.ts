import { describe, expect, it } from 'vitest'
import { buildToolRenderSpec } from './toolRenderSpec'
import type { Message } from '@/types'

function makeToolMessage(
  role: 'tool_call' | 'tool_result',
  toolName: string,
  toolInput: Record<string, unknown> | undefined,
  content = '',
): Message {
  return {
    id: 't1',
    role,
    content,
    timestamp: '2026-05-03T00:00:00Z',
    toolName,
    toolInput: toolInput === undefined ? undefined : JSON.stringify(toolInput),
  }
}

describe('buildToolRenderSpec', () => {
  it('uses the bash command as the heading when it fits within 80 characters', () => {
    const msg = makeToolMessage('tool_call', 'bash', { command: 'echo hello' })
    const spec = buildToolRenderSpec(msg)
    expect(spec.toolName).toBe('bash')
    expect(spec.heading).toBe('bash echo hello')
    expect(spec.body).toBe('')
  })

  it('truncates a bash command longer than 80 characters and appends ellipsis', () => {
    const longCommand = 'x'.repeat(120)
    const msg = makeToolMessage('tool_call', 'bash', { command: longCommand })
    const spec = buildToolRenderSpec(msg)
    expect(spec.heading).toBe('bash ' + 'x'.repeat(80) + '...')
  })

  it('uses filePath as the heading for write tool calls', () => {
    const msg = makeToolMessage('tool_call', 'write', { filePath: '/tmp/a.ts' })
    expect(buildToolRenderSpec(msg).heading).toBe('write /tmp/a.ts')
  })

  it('uses filePath as the heading for read tool calls', () => {
    const msg = makeToolMessage('tool_call', 'read', { filePath: '/tmp/b.ts' })
    expect(buildToolRenderSpec(msg).heading).toBe('read /tmp/b.ts')
  })

  it('uses filePath as the heading for edit tool calls', () => {
    const msg = makeToolMessage('tool_call', 'edit', { filePath: '/tmp/c.ts' })
    expect(buildToolRenderSpec(msg).heading).toBe('edit /tmp/c.ts')
  })

  it('uses filePath as the heading for multiedit tool calls', () => {
    const msg = makeToolMessage('tool_call', 'multiedit', { filePath: '/tmp/d.ts' })
    expect(buildToolRenderSpec(msg).heading).toBe('multiedit /tmp/d.ts')
  })

  it('uses filePath as the heading for apply_patch tool calls', () => {
    const msg = makeToolMessage('tool_call', 'apply_patch', { filePath: '/tmp/e.patch' })
    expect(buildToolRenderSpec(msg).heading).toBe('apply_patch /tmp/e.patch')
  })

  it('uses pattern as the heading for glob tool calls', () => {
    const msg = makeToolMessage('tool_call', 'glob', { pattern: '**/*.ts' })
    expect(buildToolRenderSpec(msg).heading).toBe('glob **/*.ts')
  })

  it('uses pattern as the heading for grep tool calls', () => {
    const msg = makeToolMessage('tool_call', 'grep', { pattern: 'TODO' })
    expect(buildToolRenderSpec(msg).heading).toBe('grep TODO')
  })

  it('uses name as the heading for skill_load tool calls', () => {
    const msg = makeToolMessage('tool_call', 'skill_load', { name: 'vue' })
    expect(buildToolRenderSpec(msg).heading).toBe('skill_load vue')
  })

  it('falls back to the bare tool name for unknown tools', () => {
    const msg = makeToolMessage('tool_call', 'foo_bar', { whatever: 'x' })
    expect(buildToolRenderSpec(msg).heading).toBe('foo_bar')
  })

  it('falls back to the tool name when the primary argument is missing', () => {
    const msg = makeToolMessage('tool_call', 'write', {})
    expect(buildToolRenderSpec(msg).heading).toBe('write')
  })

  it('uses message.content as the body for tool_result messages', () => {
    const msg = makeToolMessage(
      'tool_result',
      'write',
      { filePath: '/tmp/a.ts' },
      'wrote 12 bytes',
    )
    const spec = buildToolRenderSpec(msg)
    expect(spec.heading).toBe('write /tmp/a.ts')
    expect(spec.body).toBe('wrote 12 bytes')
  })

  it('returns an empty body for tool_call messages', () => {
    const msg = makeToolMessage('tool_call', 'write', { filePath: '/tmp/a.ts' }, 'ignored')
    expect(buildToolRenderSpec(msg).body).toBe('')
  })

  it('returns empty fields for non-tool messages', () => {
    const msg: Message = {
      id: 'u1',
      role: 'user',
      content: 'hello',
      timestamp: '2026-05-03T00:00:00Z',
    }
    expect(buildToolRenderSpec(msg)).toEqual({ toolName: '', heading: '', body: '' })
  })
})
