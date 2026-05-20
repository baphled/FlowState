import BashTool from '@/components/tools/BashTool.vue'
import EditTool from '@/components/tools/EditTool.vue'
import GenericTool from '@/components/tools/GenericTool.vue'
import GlobTool from '@/components/tools/GlobTool.vue'
import GrepTool from '@/components/tools/GrepTool.vue'
import ReadTool from '@/components/tools/ReadTool.vue'
import RecallSearchTool from '@/components/tools/RecallSearchTool.vue'
import TodoTool from '@/components/tools/TodoTool.vue'
import WriteTool from '@/components/tools/WriteTool.vue'
import { getToolComponent, registerTool } from './toolRegistry'

function registerIfNeeded(name: string, component: typeof BashTool): void {
  if (getToolComponent(name) === component) {
    return
  }

  registerTool({ name, component })
}

export function registerTools(): void {
  registerIfNeeded('bash', BashTool)
  registerIfNeeded('read', ReadTool)
  registerIfNeeded('write', WriteTool)
  registerIfNeeded('edit', EditTool)
  registerIfNeeded('multiedit', EditTool)
  registerIfNeeded('apply_patch', EditTool)
  registerIfNeeded('glob', GlobTool)
  registerIfNeeded('list', GlobTool)
  registerIfNeeded('grep', GrepTool)
  registerIfNeeded('skill_load', GenericTool)
  registerIfNeeded('webfetch', GenericTool)
  registerIfNeeded('websearch', GenericTool)
  registerIfNeeded('task', GenericTool)
  // Todo + recall renderers — see TodoTool.vue / RecallSearchTool.vue for
  // the per-tool rendering rules. Recall tool names map to the Go side at
  // internal/recall/query_tools.go (search_context, get_messages,
  // summarize_context) and internal/tool/recall/ (chain_search_context,
  // chain_get_messages).
  registerIfNeeded('todowrite', TodoTool)
  // `todo_update` is the per-status-transition tool the agent emits after
  // the initial `todowrite`. Its Output is shape-compatible (full JSON array
  // of {content,status,priority}) so the same renderer handles both names —
  // see internal/tool/todo/update.go:148-152 and internal/tui/intents/chat/
  // intent.go:4740-4748 for the TUI counterpart that already collapses the
  // two names onto one render path. Without this registration every
  // todo_update emission falls through MessageBubble.vue's `?? GenericTool`
  // fallback, surfacing raw call args + JSON output instead of the
  // checkbox card the user expects.
  registerIfNeeded('todo_update', TodoTool)
  registerIfNeeded('search_context', RecallSearchTool)
  registerIfNeeded('chain_search_context', RecallSearchTool)
  registerIfNeeded('get_messages', RecallSearchTool)
  registerIfNeeded('chain_get_messages', RecallSearchTool)
  registerIfNeeded('summarize_context', GenericTool)
}
