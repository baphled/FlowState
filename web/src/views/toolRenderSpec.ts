import type { Message } from '@/types'

export interface ToolRenderSpec {
  toolName: string
  heading: string
  body: string
}

const primaryArgKeys: Record<string, string> = {
  bash: 'command',
  read: 'filePath',
  write: 'filePath',
  edit: 'filePath',
  multiedit: 'filePath',
  apply_patch: 'filePath',
  glob: 'pattern',
  grep: 'pattern',
  skill_load: 'name',
}

const bashTruncateLen = 80

function parseToolInput(raw: string | undefined): Record<string, unknown> {
  if (!raw) {
    return {}
  }
  try {
    const parsed: unknown = JSON.parse(raw)
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>
    }
    return {}
  } catch {
    return {}
  }
}

function resolveHeading(toolName: string, args: Record<string, unknown>): string {
  const key = primaryArgKeys[toolName]
  if (!key) {
    return toolName
  }
  const raw = args[key]
  if (typeof raw !== 'string' || raw === '') {
    return toolName
  }
  if (toolName === 'bash' && raw.length > bashTruncateLen) {
    return raw.slice(0, bashTruncateLen) + '...'
  }
  return raw
}

/**
 * Build the canonical render spec for a tool message, mirroring the TUI
 * primary-argument map in internal/tool/display/display.go. Returns empty
 * fields for non-tool messages so callers can render a uniform shape.
 */
export function buildToolRenderSpec(message: Message): ToolRenderSpec {
  const toolName = message.toolName ?? ''
  if (!toolName) {
    return { toolName: '', heading: '', body: '' }
  }

  const args = parseToolInput(message.toolInput)
  const heading = resolveHeading(toolName, args)
  const body = message.role === 'tool_result' ? (message.content ?? '') : ''

  return { toolName, heading, body }
}
